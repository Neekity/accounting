# 业财一体化账务系统架构设计（Go + DDD）

## 1. 业务目标与质量属性

- **目标**：建设一套支持多业务场景的业财一体化账务系统，自动接收外部业务消息、匹配会计规则并生成/重生成凭证。
- **关键质量属性**：
  - **高可用**：无状态计算节点水平扩展，核心基础设施具备故障转移能力。
  - **强一致**：消息状态、凭证及分录在同一事务内落库，提供精准重放能力。
  - **可追溯**：所有凭证生成、冲销、重建过程可被审计和回放。
  - **可扩展**：规则库与领域模型支持多准则、多币种及新增业务类型。

## 2. DDD 视角下的领域划分

```
/internal
  /app            // 应用服务层
  /domain         // 领域层
    /message      // 业务消息上下文
    /rule         // 凭证规则上下文
    /voucher      // 凭证上下文
  /infrastructure // 基础设施层
  /interfaces     // 接口适配层（HTTP/MQ）
/cmd
  /accounting-api
```

### 2.1 边界上下文

| 上下文 | 核心职责 | 聚合 | 关键实体/值对象 |
|--------|----------|------|----------------|
| Message Context | 接入外部业务消息、完成幂等校验与处理编排 | `InboundMessage` | MessageID、Payload、MessageStatus、IdempotencyKey |
| Rule Context | 维护凭证规则、匹配策略与版本发布 | `VoucherRule` | RuleID、Condition, Template, Version |
| Voucher Context | 依据规则生成凭证、分录并管理生命周期 | `Voucher` | VoucherID、Entries、SourceMessageID、Status、Version |

上下文之间通过应用服务或防腐层交互，领域模型彼此保持独立。

### 2.2 聚合、实体与领域服务

- `InboundMessage` 聚合
  - 实体：来源系统、业务单号、消息体、幂等键、状态、版本。
  - 行为：幂等校验、状态流转、生成重放任务、记录失败原因。
- `VoucherRule` 聚合
  - 实体：适用场景、触发条件、凭证模板、版本、发布状态、优先级。
  - 行为：条件匹配、模板装配、版本切换与灰度控制。
- `Voucher` 聚合
  - 实体：凭证号、日期、币种、分录列表、来源消息、生成时间、状态、版本。
  - 值对象：`Entry`（借贷方向、科目、金额、摘要）、`AccountingSubject`、`Money`、`RegenerationTrace`。
  - 行为：分录平衡校验、冲销、重建、审计记录。
- 关键领域服务
  - `RuleMatchingService`：根据消息关键字段（`transType`、`amount_type`、渠道、币种等）命中规则。
  - `VoucherFactory`：将规则模板与消息数据映射为凭证与分录。
  - `VoucherRegenerationService`：处理重建/冲销逻辑并保持凭证版本链。

## 3. 消息驱动流程

### 3.1 标准处理链路

1. 外部系统调用 `POST /api/v1/messages` 推送消息（签名 + 幂等键校验）。
2. `MessageAppService` 写入 `InboundMessage` 仓储并发布 `MessageAccepted` 领域事件（Outbox Pattern）。
3. `VoucherGenerationHandler` 订阅事件，加载消息聚合并执行规则匹配。
4. `VoucherFactory` 生成凭证及分录，应用层开启事务：
   - 更新消息状态 → `Processing/Completed`。
   - 持久化凭证与分录。
   - 写入审计日志与 Outbox 消息。
5. 事务提交后异步发布 `VoucherGenerated` 事件，用于通知下游或刷新缓存。
6. 处理失败时更新状态为 `Failed`，记录错误并供重试任务消费。

### 3.2 幂等与并发控制

- 消息入库采用唯一索引 `(MessageID, SourceSystem, Version)`；重复提交直接返回历史结果。
- `InboundMessage` 在处理期间加行级锁或乐观锁版本号，防止并发处理。
- 重放操作使用 `RegenerationRequestID` 做幂等控制。

## 4. 凭证规则匹配与模板

- 规则模板以 DSL/JSON 形式定义：
  ```json
  {
    "rule_id": "loan_repayplan_principal",
    "conditions": {"transType": "loan_repayplan", "amount_type": "principal"},
    "entry_template": [
      {"side": "Debit", "subject": "1221.01.01", "amount": "payload.amount"},
      {"side": "Credit", "subject": "1012.X.02", "amount": "payload.amount"}
    ],
    "posting_date": "payload.actual_loan_date"
  }
  ```
- 匹配策略：优先级 + 精确条件 → 通配符 → 默认规则。
- 规则缓存：发布后写入 Redis/Config Center，节点按版本号刷新。
- 复杂映射（如多行分录、金额拆分）可在模板中支持表达式或嵌套子模板。

## 5. 凭证重生成策略

1. 提供 `POST /api/v1/messages/{id}/rebuild` 和后台批量重放任务。
2. 重建流程：
   - 锁定原消息与凭证，确认规则版本（历史/最新）。
   - 标记原凭证为 `Superseded` 或生成反向凭证。
   - 生成新凭证并记录 `RegenerationTrace`（触发人、原因、时间、使用规则版本）。
3. 审计：保留所有重建链路，支持根据 `RegenerationRequestID` 回查。
4. 幂等：重复的重建请求返回已有凭证编号。

## 6. 高可用与一致性设计

- **数据库事务**：使用关系型数据库（PostgreSQL/MySQL），应用层通过事务脚本一次性写入消息状态、凭证、分录、审计。
- **Outbox Pattern**：凭证生成事件写入 outbox 表，由独立 worker 发布到 Kafka/NATS，避免分布式事务。
- **灾备**：数据库主从 + 自动故障转移；对象存储备份模板与审计文档。
- **降级策略**：规则缓存失效时回退到数据库查询；缓存刷新失败触发告警但不阻断处理。
- **重试机制**：
  - 消息处理失败记录错误并由后台任务按指数退避重试。
  - 外部事件投递失败由 outbox worker 重试，超过阈值进入告警队列。

## 7. 基础设施设计

| 组件 | 选型建议 | 作用 |
|------|----------|------|
| API Gateway | Kong / Nginx Ingress | 统一接入、限流、鉴权 |
| 应用服务 | Go（Gin/Echo） | 暴露 REST API、执行业务用例 |
| 数据库 | PostgreSQL / MySQL | 持久化消息、凭证、规则、审计 |
| 缓存 | Redis | 规则缓存、幂等 Token、分布式锁 |
| 消息总线 | Kafka / NATS JetStream | 领域事件、异步通知、补偿流程 |
| 对象存储 | MinIO / S3 | 存放模板快照、审计文件 |
| 监控 | Prometheus + Grafana | 指标监控、告警 |
| 日志 | ELK / Loki | 查询链路日志、审计追踪 |

## 8. Go 实现蓝图

### 8.1 关键接口

```go
// domain/message
type Repository interface {
    Save(ctx context.Context, msg *InboundMessage) error
    FindByID(ctx context.Context, id MessageID) (*InboundMessage, error)
    LockForProcess(ctx context.Context, id MessageID) (*InboundMessage, error)
}

// domain/rule
type Matcher interface {
    Match(ctx context.Context, msg *InboundMessage) (*VoucherRule, error)
}

// domain/voucher
type VoucherRepository interface {
    Save(ctx context.Context, v *Voucher) error
    FindByMessage(ctx context.Context, id MessageID) ([]*Voucher, error)
}
```

### 8.2 应用服务示例

```go
func (s *VoucherService) Generate(ctx context.Context, msgID message.ID) error {
    return s.tx.WithinTransaction(ctx, func(txCtx context.Context) error {
        msg, err := s.messages.LockForProcess(txCtx, msgID)
        if err != nil {
            return err
        }
        rule, err := s.matcher.Match(txCtx, msg)
        if err != nil {
            return s.failMessage(txCtx, msg, err)
        }
        voucher, err := s.factory.Create(rule, msg)
        if err != nil {
            return s.failMessage(txCtx, msg, err)
        }
        if err := s.vouchers.Save(txCtx, voucher); err != nil {
            return err
        }
        msg.MarkCompleted()
        if err := s.messages.Save(txCtx, msg); err != nil {
            return err
        }
        return s.outbox.Publish(txCtx, events.VoucherGenerated{VoucherID: voucher.ID})
    })
}
```

### 8.3 交互适配

- `interfaces/http`: DTO、校验、幂等 Token 校验、错误映射。
- `infrastructure/persistence`: 使用 `sqlc`/`gorm` 实现仓储接口，封装事务管理。
- `infrastructure/messaging`: Outbox 轮询、事件发布、重试策略。
- `pkg/shared`: 公共错误、Money/Decimal、时区工具。

## 9. 数据模型草图

| 表 | 关键字段与说明 |
|----|----------------|
| `inbound_messages` | `id`, `source_system`, `business_doc_no`, `payload`, `idempotency_key`, `status`, `version`, `error`, `created_at`, `updated_at` |
| `voucher_rules` | `id`, `biz_type`, `condition_expr`, `template`, `priority`, `version`, `status`, `effective_from`, `effective_to`, `publisher` |
| `vouchers` | `id`, `voucher_no`, `source_message_id`, `rule_id`, `status`, `version`, `issued_at`, `superseded_by`, `created_at` |
| `voucher_entries` | `id`, `voucher_id`, `line_no`, `account_code`, `debit_credit`, `amount`, `currency`, `summary` |
| `voucher_audit_logs` | `id`, `voucher_id`, `action`, `operator`, `reason`, `regeneration_request_id`, `created_at`, `metadata` |
| `outbox` | `id`, `aggregate_type`, `aggregate_id`, `event_type`, `payload`, `status`, `created_at`, `available_at` |

## 10. API 设计概览

| 方法 | 路径 | 描述 |
|------|------|------|
| `POST` | `/api/v1/messages` | 接收业务消息并返回受理状态/历史结果 |
| `GET` | `/api/v1/messages/{id}` | 查询消息处理状态及关联凭证 |
| `POST` | `/api/v1/messages/{id}/rebuild` | 触发单条消息重建凭证 |
| `POST` | `/api/v1/regeneration` | 批量重放（按业务单号/时间范围） |
| `GET` | `/api/v1/vouchers/{id}` | 查询凭证详情及版本链 |
| `POST` | `/api/v1/rules` | 创建/更新凭证规则 |
| `POST` | `/api/v1/rules/{id}/publish` | 发布规则版本并通知刷新 |

安全与治理：使用 OAuth2/JWT、请求签名、字段级加密；重要操作写入审计日志。

## 11. 运行保障与可观测性

- **监控指标**：消息吞吐、成功率、延迟、规则匹配耗时、数据库事务耗时、重试次数。
- **日志追踪**：Correlation ID 贯穿消息 → 凭证 → 事件，支持分布式追踪。
- **告警策略**：失败率超过阈值、Outbox 堆积、规则缓存刷新失败、重试超限。
- **运维工具**：
  - 后台审计界面查看消息/凭证/重建历史。
  - 数据对账工具比对通道流水与凭证。
  - 配置中心回滚与灰度能力。

## 12. 部署与 DevOps

1. **CI/CD**：Go 模块编译 → 单元测试 → 静态检查 → 容器构建 → 集成测试 → 分阶段发布。
2. **灰度发布**：规则版本支持灰度；服务端采用金丝雀或蓝绿部署。
3. **数据迁移**：使用迁移工具（`golang-migrate`）管理 DDL，支持回滚。
4. **安全合规**：敏感数据脱敏、审计日志不可篡改、遵循财务数据保留策略。
5. **灾备演练**：定期演练数据库主备切换、消息堆积恢复、Outbox 补偿。

## 13. 放款/还款场景凭证规则速览

### 13.1 放款阶段

| 场景 | `transType` | 借方科目 | 贷方科目 | 数据来源 | 备注 |
|------|-------------|----------|----------|----------|------|
| RDL → Escrow 备款 | `fund_prepare` *(待确认)* | 1011.02.99 业务资金-BNI 020-待处理 | 1011.01.99 业务资金-BNI RDL-待处理 | 调拨指令 | 建议在调拨系统补充 `transType`。 |
| Escrow → 借款人放款 | `channel_payout` | 1012.X.02 其他货币资金-通道/账户-放款 | 1011.02.99 业务资金-BNI 020-待处理 | 通道放款回执 | 与 To B 放款指令对账。 |
| 通道流水打标 | `channel_mark_loan` | 1012.X.01 其他货币资金-通道/账户-通道余额 | 1012.X.02 其他货币资金-通道/账户-放款 | 通道流水打标 | -- |
| 银行余额调整（RDL） | `bank_adjustment_rdl` | 1011.01.99 业务资金-BNI RDL-待处理 | 1011.01.01 业务资金-BNI RDL-银行余额 | 银行流水 | -- |
| 银行余额调整（020） | `bank_adjustment_escrow` | 1011.02.99 业务资金-BNI 020-待处理 | 1011.02.01 业务资金-BNI 020-银行余额 | 银行流水 | -- |
| 生成还款计划-本金 | `loan_repayplan` (`amount_type=principal`) | 1221.01.01 应收账款-未到期-应收本金 | 1012.X.02 其他货币资金-通道/账户-放款 | To B 数据 | 记账日期=实际放款日。 |
| 生成还款计划-利息 | `loan_repayplan` (`amount_type=interest`/`interest_tax`) | 1221.01.02 应收账款-未到期-应收利息 | 6001.03.01 营业收入-未到期收入-利息收入 | To B 数据 | 放款口径不计罚息。 |
| 生成还款计划-服务费 | `loan_repayplan` (`amount_type=fin_service`/`tax_fin_service`) | 1221.01.03 应收账款-未到期-应收服务费 | 6001.03.02 营业收入-未到期收入-服务费收入 | To B 数据 | VAT 同步确认，后续转出。 |
| 到期口径重分类 | `loan_repayplan` (`amount_type=principal`) | 1221.02.01 应收账款-已到期-应收本金 | 1221.01.01 应收账款-未到期-应收本金 | To B 数据 | 记账日期=预计还款日。 |
| 到期口径收入转出 | `loan_repayplan` (`amount_type=interest`/`fin_service`/`lateinterest`) | 6001.03.0X 未到期收入 | 1221.01.0X 未到期应收 | To B 数据 | `lateinterest_tax` 需在数据层确认。 |
| 到期口径收入入账 | `loan_repayplan` (`amount_type=interest`/`fin_service`/`lateinterest`) | 1221.02.0X 已到期应收 | 6001.02.0X 已到期收入 | To B 数据 | -- |
| 放款冲正 | `loan_reverse` | 反向分录 | 反向分录 | 取数逻辑同上 | 与原金额一致，方向相反。 |

### 13.2 代偿前还款与清分

| 场景 | `transType` | 借方科目 | 贷方科目 | 数据来源 | 备注 |
|------|-------------|----------|----------|----------|------|
| 借款人还款入通道 | `repay_before_compensate` (`amount_type=aggregate`) | 1012.X.03 通道-还款 | 1221.03 应收账款-清分 | 通道回执 | -- |
| 通道流水打标 | `channel_mark_repay` | 1012.X.01 通道余额 | 1012.X.03 通道-还款 | 通道流水打标 | -- |
| 通道结算拆分（本息/费用/税费/拨备/滋缴） | `channel_settle_*` | 1011.02.99 业务资金-BNI 020-待处理 | 1012.X.0[4-9] 通道拆分科目 | 通道结算指令 | 拨备/滋缴留在通道，不提现。 |
| 结算后流水打标 | `channel_mark_settlement` | 1012.X.0[4-9] 通道拆分科目 | 1012.X.01 通道余额 | 通道流水打标 | -- |
| 银行余额调整 | `bank_adjustment_escrow` | 1011.02.01 业务资金-BNI 020-银行余额 | 1011.02.99 业务资金-BNI 020-待处理 | 银行流水 | -- |
| 清分-本金/利息/服务费/罚息 | `repay_before_compensate` (`amount_type`=分别对应) | 1221.03 应收账款-清分 | 1221.02.0X 已到期应收 | To B 清分结果 | 利息税、服务费 VAT 同步生成。 |
| 清分-滋缴金 | `overflow` | 1221.03 应收账款-清分 | 2241.05 其他应付款-滋缴金 | To B 清分结果 | -- |
| 实收收入确认 | `repay_before_compensate` (`amount_type=interest/fin_service/lateinterest`) | 6001.02.0X 已到期收入 | 6001.01.0X 已实现收入 | To B 清分结果 | -- |
| 税费确认 | `repay_before_compensate` (`amount_type=interest_tax/tax_fin_service/lateinterest_tax`) | 6403.02 税金及附加-利息税 / 6001.01.02 未到期收入-服务费 | 2221.XX 应交税费 | To B 清分结果 | VAT/利息税流向明确。 |
| 减免/优惠券 | `loan_decrease` (`amount_type`=各类) | 6001.01.04 已实现收入-减免/优惠券 | 1221.02.0X 已到期应收 | To B 清分结果 | -- |

### 13.3 特殊场景

| 场景 | `transType` | 借方科目 | 贷方科目 | 说明 |
|------|-------------|----------|----------|------|
| 提前还款重分类 | `repay_reclass_early` | 同到期口径分录 | 相反方向 | 清分系统在拆分前补发消息，将未到期余额转入已到期。 |
| Escrow 自动提现至 RDL | `escrow_auto_withdraw` | 1011.01.99 业务资金-BNI RDL-待处理 | 1011.02.99 业务资金-BNI 020-待处理 | 自动提现事件驱动。 |
| 服务费/VAT/税金转运营资金 | `escrow_to_operation` | 2241.04 其他应付款-通道提现 | 1011.02.99 业务资金-BNI 020-待处理 | 与财务系统对账抵消。 |
| 滋缴金退款 | `overflow_refund` | 2241.05 其他应付款-滋缴金 | 1011.02.99 业务资金-BNI 020-待处理 | 借款人退款指令。 |

### 13.4 审计与再处理补充

- 凭证与消息一一关联，重建时写入 `RegenerationTrace` 与 `SupersededBy`。
- 罚息统一在清分阶段处理，放款口径不重复确认；`lateinterest_tax` 字段需在上游确认，若缺失需扩展消息结构。
- VAT/利息税在收入确认与税金缴纳两个环节均有凭证，需配置 `tax_payment` 类规则闭环管理。
- 拨备/滋缴虚户资金默认留在通道内，如需提现需新增 `transType` 并走审批流程。

---

该架构设计通过 DDD 划分领域边界，结合事务性处理、Outbox、规则模板化及完善的重建机制，满足业财一体化系统对高可用、强一致与可追溯的核心诉求。
