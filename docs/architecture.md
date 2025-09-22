# 业财一体化账务系统架构设计（Golang + DDD）

## 1. 目标与范围

本设计旨在构建一套支持企业业财一体化的账务系统，使用 Go 语言实现，并贯彻领域驱动设计（DDD）思想。系统通过对接外部业务系统推送的消息生成会计凭证，要求具备高可用、高一致性，并且支持针对每一条消息的凭证重新生成能力。

## 2. 核心业务场景

1. **业务消息接入**：外部业务系统通过 API 推送业务事件消息到账务系统。
2. **凭证类型识别**：根据消息中的关键字段匹配会计凭证类型规则。
3. **凭证生成**：依照匹配到的规则生成会计凭证及分录。
4. **凭证重生成**：支持对某条消息或业务单据重新生成凭证，需保证数据一致性及审计追溯。

## 3. DDD 分层与边界上下文

```
/internal
  /app            // 应用服务层
  /domain         // 领域层
    /voucher      // 凭证上下文
    /message      // 业务消息上下文
    /rule         // 凭证规则上下文
  /infrastructure // 基础设施层
  /interfaces     // 用户接口层（REST/gRPC）
/cmd
  /accounting-api
```

- **领域层（Domain）**：包含聚合、实体、值对象、领域服务、领域事件。
- **应用层（Application）**：编排用例，调用领域服务与仓储。
- **基础设施层（Infrastructure）**：实现仓储、消息总线、数据库访问、外部服务适配器。
- **接口层（Interfaces）**：暴露 REST/gRPC API、消息队列消费端。

定义三个主要的边界上下文：

| 上下文 | 责任 | 聚合 | 关键实体/值对象 |
|--------|------|------|----------------|
| Message Context | 接入与管理外部业务消息，提供去重、幂等、状态跟踪 | `InboundMessage` | MessageID、Payload、MessageStatus |
| Rule Context | 维护凭证规则、匹配策略及规则版本 | `VoucherRule` | RuleID、TriggerFields、DebitCreditTemplate |
| Voucher Context | 根据规则生成凭证、分录，保障凭证状态生命周期 | `Voucher` | VoucherID、Entries、SourceMessageID、Version |

上下文之间通过领域服务或防腐层（ACL）交互，避免耦合。

## 4. 领域模型概述

### 4.1 聚合与实体

- `InboundMessage`（消息聚合）
  - 实体：MessageID、来源系统、业务单据号、消息体、幂等键、状态。
  - 行为：校验幂等、锁定处理、标记处理结果、生成重放任务。
- `VoucherRule`（规则聚合）
  - 实体：规则标识、适用业务场景、触发字段条件、分录模板、版本、状态。
  - 行为：匹配、版本切换、模板转换。
- `Voucher`（凭证聚合）
  - 实体：凭证号、凭证日期、币种、分录列表、来源消息、生成时间、状态、版本。
  - 值对象：`Entry`（借贷方向、科目、金额）、`AccountingSubject`、`Money`。
  - 行为：分录校验、冲销/重生、状态流转、生成领域事件。

### 4.2 领域服务

- `RuleMatchingService`：根据消息中的关键字段（如业务类型、客户、金额区间等）查询规则库，返回适用的 `VoucherRule`。
- `VoucherFactory`：组合规则模板与消息数据，生成完整凭证。
- `VoucherRegenerationService`：在重生成流程中，处理凭证冲销、复制、重放。

### 4.3 领域事件

- `MessageAccepted`、`VoucherGenerated`、`VoucherGenerationFailed`、`VoucherRegenerated`。事件通过事件总线发布用于审计、通知、后续异步处理。

## 5. 核心流程设计

### 5.1 消息接入与幂等

1. 接口层暴露 `/api/v1/messages` 接口，接收业务系统推送的 JSON 消息。
2. 应用服务 `MessageAppService` 校验请求签名与幂等键（MessageID + SourceSystem + Version）。
3. 将消息持久化至 `InboundMessage` 仓储，状态标记为 `Received`。
4. 发布 `MessageAccepted` 领域事件，驱动凭证生成流程。

幂等实现要点：
- 在数据库中通过唯一索引保证同一幂等键只能持久化一次。
- 支持查询消息状态，以告知外部系统处理结果。

### 5.2 凭证生成流程

1. `VoucherGenerationHandler`（应用服务）监听 `MessageAccepted` 事件。
2. 加载 `InboundMessage` 聚合，调用 `RuleMatchingService` 获取规则。
3. 根据规则模板与消息体字段，使用 `VoucherFactory` 创建 `Voucher` 聚合。
4. 通过事务性仓储将凭证与分录写入数据库，并更新消息状态为 `Completed`。
5. 若生成失败，记录失败原因并更新状态为 `Failed`，支持重试。

### 5.3 凭证重生成

1. 提供 `/api/v1/messages/{id}/rebuild` 接口或后台任务触发。
2. `VoucherRegenerationService` 加载原凭证与消息，确认当前规则版本：
   - 若业务要求重放旧规则，可按原规则版本生成新凭证并保留历史版本。
   - 若需要使用最新规则，重新执行匹配流程。
3. 对原凭证执行冲销（生成反向凭证）或标记为 `Superseded`。
4. 生成新凭证并记录版本号、重建来源、操作人。
5. 全流程需写入审计日志，并通过事件驱动通知下游系统。

### 5.4 规则管理

- 提供独立的规则管理接口或后台服务，支持规则的 CRUD、上下线、版本控制。
- 规则变更发布后生成领域事件，通知缓存刷新或分布式配置中心。
- 匹配算法可使用优先级 + 条件树（DSL）实现，支持灰度发布。

## 6. 高可用与高一致性设计

### 6.1 部署架构

- **API 网关 + 多实例服务**：接口层部署为无状态 Go 服务，后端使用 Kubernetes 或服务编排实现水平扩展。
- **数据库**：选择支持强一致性的关系型数据库（PostgreSQL/MySQL），使用主从复制与自动故障转移。
- **消息总线**：使用 Kafka/NATS 作为事件总线，采用至少一次投递，结合幂等消费确保一致性。
- **缓存**：在规则匹配环节使用 Redis 缓存已发布规则，配合版本号与发布事件控制刷新。

### 6.2 事务与一致性策略

- 应用层采用 **事务脚本 + 聚合根锁** 模式，在数据库事务内完成消息状态、凭证写入，保证强一致。
- 通过 **Outbox Pattern** 发布领域事件，避免事务与消息队列的分布式事务问题。
- 支持 **补偿机制**：凭证生成失败时记录失败状态，提供后台任务重试或人工处理。
- 实现 **读写分离** 时需保证查询场景可接受最终一致性，否则关键查询走主库。

### 6.3 幂等与重放

- 对每条消息分配全局唯一 `MessageID`，并存储处理版本。
- `Voucher` 聚合包含 `SourceMessageID` 和 `Version`，支持追溯。
- 重放流程使用显式 `RegenerationRequestID`，确保重放操作本身幂等。

### 6.4 审计与监控

- 记录凭证生成、冲销、重放的审计日志，包含操作人、时间、原因。
- 集成 Prometheus + Grafana 监控：请求量、成功率、延迟、规则匹配耗时、数据库事务耗时。
- 使用集中式日志（ELK）跟踪跨服务调用。

## 7. Go 项目结构示例

```
/cmd
  /accounting-api
    main.go
/internal
  /interfaces
    /http
      handler.go
      dto.go
  /app
    message_service.go
    voucher_service.go
  /domain
    /message
      inbound_message.go
      repository.go
      events.go
    /rule
      voucher_rule.go
      repository.go
      matcher.go
    /voucher
      voucher.go
      entries.go
      repository.go
      events.go
  /infrastructure
    /persistence
      message_repository.go
      voucher_repository.go
      rule_repository.go
    /messaging
      event_publisher.go
    /config
      config.go
/pkg
  /shared
    errors.go
    money.go
```

- 每个上下文封装其聚合、仓储接口与领域服务。
- 应用层服务负责协调多个上下文、处理事务。
- 基础设施层实现仓储接口（例如使用 `gorm` 或 `sqlx`）。

## 8. 数据库模型草图

- `inbound_messages`
  - `id`, `source_system`, `business_doc_no`, `payload`, `idempotency_key`, `status`, `version`, `created_at`, `updated_at`。
- `voucher_rules`
  - `id`, `name`, `biz_type`, `condition_expr`, `template`, `version`, `status`, `effective_from`, `effective_to`。
- `vouchers`
  - `id`, `voucher_no`, `source_message_id`, `rule_id`, `status`, `version`, `issued_at`, `created_at`。
- `voucher_entries`
  - `id`, `voucher_id`, `line_no`, `account_code`, `debit_credit`, `amount`, `currency`, `summary`。
- `voucher_audit_logs`
  - `id`, `voucher_id`, `action`, `operator`, `reason`, `created_at`, `metadata`。

关键约束：
- `inbound_messages(idempotency_key)` 唯一索引。
- `vouchers(voucher_no)` 唯一索引。
- 通过外键保证凭证与分录一致性。

## 9. API 设计概述

| 方法 | 路径 | 描述 |
|------|------|------|
| `POST` | `/api/v1/messages` | 业务系统推送消息，返回消息受理状态 |
| `GET` | `/api/v1/messages/{id}` | 查询消息处理状态与生成的凭证信息 |
| `POST` | `/api/v1/messages/{id}/rebuild` | 触发凭证重生成 |
| `GET` | `/api/v1/vouchers/{id}` | 查询凭证详情 |
| `POST` | `/api/v1/rules` | 创建/更新凭证规则 |
| `POST` | `/api/v1/rules/{id}/publish` | 发布规则版本 |

接口安全：使用 OAuth2/JWT + 签名校验，重要操作需审计。

## 10. 可扩展性与未来演进

- **多会计准则**：凭证规则模板可根据会计准则扩展，使用策略模式选择不同准则。
- **多币种**：`Money` 值对象支持币种与精度，结合汇率服务。
- **自动对账**：后续可引入对账上下文，与财务报表系统集成。
- **事件溯源**：若需要更强追溯，可将凭证生成过程事件化，记录状态变化。

## 11. 非功能性要求

- **高可用**：服务无状态，支持水平扩展；数据库与消息中间件具备故障转移；关键流程提供降级与熔断。
- **高一致性**：凭证生成与消息状态更新在单事务内完成；事件发布使用事务性 outbox；重放流程需全链路幂等。
- **安全合规**：日志脱敏、访问控制、遵循财务数据保留政策。
- **性能**：单条消息处理延迟 < 200ms（含规则匹配与数据库写入），支持峰值 1000 TPS。

## 12. 技术选型建议

- Web 框架：`gin` / `echo`。
- 数据访问：`sqlc` + PostgreSQL 或 `gorm`。
- 消息中间件：Kafka / NATS JetStream。
- 配置中心：Apollo / Consul。
- 依赖注入：`google/wire` 或轻量自实现。
- 测试：`testify`、BDD 框架用于领域层单元测试。

## 13. 交付策略

1. **原型阶段**：实现核心消息接入与凭证生成流程，完成领域模型雏形。
2. **增强阶段**：完善规则管理、重生成流程、审计与监控。
3. **高可用阶段**：部署高可用集群，加入事件总线、监控告警。
4. **持续优化**：根据业务反馈调优规则引擎、扩展多场景支持。

## 14. 放款与还款凭证业务逻辑详解

### 14.1 信息流概览

- **业务触发源**：To B 业务系统（放款、还款计划、优惠）、通道清结算系统（实际流水与结算拆分）、银行流水（余额调整）。
- **消息载体**：统一通过 `/api/v1/messages` 推送，消息体中包含 `transType`、`amount_type`、`transFinishAt`/计划日期等关键信息。
- **规则选择**：`RuleMatchingService` 根据 `transType`、资产编号、渠道、币种等字段命中凭证模板；若存在空白场景（如 1.1 备款），需新增 `transType`（建议 `fund_prepare`）并配置规则。
- **凭证生成与再处理**：应用层在单事务内写入凭证、分录与消息状态，同时记录消息与凭证的对照关系，确保按消息维度可重放。
- **信息流补图**：`voucher_uml.puml` 更新为跨系统信息流图，每条主干流程均在箭头处标注 `transType`，展示了业务系统/通道/银行到账务域的消息流向与数据存储。

### 14.2 分录规则总览

> 说明：以下表格补充了每一步的 `transType`、借贷科目、数据来源与备注（包含原问题的解答/待确认项）。未明确的 `transType` 给出建议命名，便于后续规则上线。

#### 14.2.1 放款阶段（含备款、放款、收入确认）

| 场景 | `transType` | 借方科目 | 贷方科目 | 数据来源 | 备注 |
|------|-------------|----------|----------|----------|------|
| 1.1 备款（RDL → Escrow 调拨） | `fund_prepare` *(待确认)* | 1011.02.99 业务资金-BNI 020-待处理 | 1011.01.99 业务资金-BNI RDL-待处理 | 调拨指令（待定义） | 缺少消息源，建议在资金调拨系统落地消息并补充 `transType`。 |
| 1.1 Escrow 放款至用户 | `channel_payout` *(建议)* | 1012.X.02 其他货币资金-通道/账户-放款 | 1011.02.99 业务资金-BNI 020-待处理 | 通道放款回执 | 与 To B 放款指令对账，便于日终核对。 |
| 1.1 T+1 通道流水打标 | `channel_mark_loan` | 1012.X.01 其他货币资金-通道/账户-通道余额 | 1012.X.02 其他货币资金-通道/账户-放款 | 通道流水打标 | 归集到通道余额。 |
| 1.1 银行余额调整（RDL） | `bank_adjustment_rdl` | 1011.01.99 业务资金-BNI RDL-待处理 | 1011.01.01 业务资金-BNI RDL-银行余额 | 银行流水打标 | 银行账单 T+1。 |
| 1.1 银行余额调整（020） | `bank_adjustment_escrow` | 1011.02.99 业务资金-BNI 020-待处理 | 1011.02.01 业务资金-BNI 020-银行余额 | 银行流水打标 | -- |
| 1.2 放款生成还款计划 | `loan_repayplan` (`amount_type=principal`) | 1221.01.01 应收账款-未到期-应收本金 | 1012.X.02 其他货币资金-通道/账户-放款 | To B 数据 | 记账日期=实际放款时间。`transFinishAt` 可作为兜底字段。 |
| 1.2 T+1 通道流水打标 | `channel_mark_loan` | 1012.X.02 其他货币资金-通道/账户-放款 | 1012.X.01 其他货币资金-通道/账户-通道余额 | 通道流水打标 | 与 1.1 同步。 |
| 1.2.1 放款口径利息收入 | `loan_repayplan` (`amount_type=interest`/`interest_tax`) | 1221.01.02 应收账款-未到期-应收利息 | 6001.03.01 营业收入-未到期收入-利息收入 | To B 数据 | 罚息不计入放款口径；真实罚息（逾期产生）在 `lateinterest` 流程中处理。VAT/利息税在清分时转入应交税费。 |
| 1.2.1 放款口径服务费收入 | `loan_repayplan` (`amount_type=fin_service`/`tax_fin_service`) | 1221.01.03 应收账款-未到期-应收服务费 | 6001.03.02 营业收入-未到期收入-服务费收入 | To B 数据 | VAT（`tax_fin_service`）同步入账，在清分阶段转出至应交税费。 |
| 1.2.2 到期口径本金重分类 | `loan_repayplan` (`amount_type=principal`) | 1221.02.01 应收账款-已到期-应收本金 | 1221.01.01 应收账款-未到期-应收本金 | To B 数据 | 记账日期=预计还款时间。 |
| 1.2.2 到期口径利息收入转出 | `loan_repayplan` (`amount_type=interest`/`interest_tax`) | 6001.03.01 营业收入-未到期收入-利息收入 | 1221.01.02 应收账款-未到期-应收利息 | To B 数据 | -- |
| 1.2.2 到期口径服务费收入转出 | `loan_repayplan` (`amount_type=fin_service`/`tax_fin_service`) | 6001.03.02 营业收入-未到期收入-服务费收入 | 1221.01.03 应收账款-未到期-应收服务费 | To B 数据 | -- |
| 1.2.2 到期口径罚息收入转出 | `loan_repayplan` (`amount_type=lateinterest`/`lateinterest_tax`) | 6001.03.03 营业收入-未到期收入-罚息收入 | 1221.01.04 应收账款-未到期-应收罚息 | To B 数据 | `lateinterest_tax` 需在 To B 数据中确认字段；若不存在需在消息层扩展。 |
| 1.2.2 到期口径利息入账 | `loan_repayplan` (`amount_type=interest`/`interest_tax`) | 1221.02.02 应收账款-已到期-应收利息 | 6001.02.01 营业收入-已到期收入-利息收入 | To B 数据 | -- |
| 1.2.2 到期口径服务费入账 | `loan_repayplan` (`amount_type=fin_service`/`tax_fin_service`) | 1221.02.03 应收账款-已到期-应收服务费 | 6001.02.02 营业收入-已到期收入-服务费收入 | To B 数据 | -- |
| 1.2.2 到期口径罚息入账 | `loan_repayplan` (`amount_type=lateinterest`/`lateinterest_tax`) | 1221.02.04 应收账款-已到期-应收罚息 | 6001.02.03 营业收入-已到期收入-罚息收入 | To B 数据 | -- |
| 1.2.3 放款冲正（失败） | `loan_reverse` | 反向分录 | 反向分录 | 与 1.2~1.2.2 相同 | 与原分录金额相同、方向相反，重建时走同一规则。 |

#### 14.2.2 代偿前还款与清分

| 场景 | `transType` | 借方科目 | 贷方科目 | 数据来源 | 备注 |
|------|-------------|----------|----------|----------|------|
| 2.1 通道收到还款 | `repay_before_compensate` (`amount_type=aggregate`) | 1012.X.03 其他货币资金-通道/账户-还款 | 1221.03 应收账款-清分 | 通道实时回执 | -- |
| 2.1 通道流水打标 | `channel_mark_repay` | 1012.X.01 其他货币资金-通道/账户-通道余额 | 1012.X.03 其他货币资金-通道/账户-还款 | 通道流水打标 | -- |
| 2.1 通道结算至业务资金（本息） | `channel_settle_principal_interest` | 1011.02.99 业务资金-BNI 020-待处理 | 1012.X.04 其他货币资金-通道/账户-本息 | 通道结算指令 | -- |
| 2.1 通道结算至业务资金（服务费） | `channel_settle_service_fee` | 1011.02.99 业务资金-BNI 020-待处理 | 1012.X.05 其他货币资金-通道/账户-服务费 | 通道结算指令 | -- |
| 2.1 通道结算至业务资金（利息/罚息税） | `channel_settle_interest_tax` | 1011.02.99 业务资金-BNI 020-待处理 | 1012.X.06 其他货币资金-通道/账户-利息罚息税 | 通道结算指令 | -- |
| 2.1 通道结算至业务资金（VAT） | `channel_settle_vat` | 1011.02.99 业务资金-BNI 020-待处理 | 1012.X.07 其他货币资金-通道/账户-VAT | 通道结算指令 | -- |
| 2.1 通道结算至拨备虚户 | `channel_settle_provision` | 1011.02.99 业务资金-BNI 020-待处理 | 1012.X.08 其他货币资金-通道/账户-拨备虚户 | 通道结算指令 | 拨备/滋缴虚户留在通道内，不自动提现。 |
| 2.1 通道结算至滋缴金虚户 | `channel_settle_overflow` | 1011.02.99 业务资金-BNI 020-待处理 | 1012.X.09 其他货币资金-通道/账户-滋缴款虚户 | 通道结算指令 | -- |
| 2.1 通道流水打标（结算后） | `channel_mark_settlement` | 1012.X.0[4-9] 其他货币资金-通道/账户-拆分科目 | 1012.X.01 其他货币资金-通道/账户-通道余额 | 通道流水打标 | 针对本息/服务费/税费/拨备/滋缴金分别生成分录。 |
| 2.1 银行余额调整 | `bank_adjustment_escrow` | 1011.02.01 业务资金-BNI 020-银行余额 | 1011.02.99 业务资金-BNI 020-待处理 | 银行流水打标 | -- |
| 2.2 清分-本金 | `repay_before_compensate` (`amount_type=principal`) | 1221.03 应收账款-清分 | 1221.02.01 应收账款-已到期-应收本金 | To B 清分结果 | -- |
| 2.2 清分-利息 | `repay_before_compensate` (`amount_type=interest`/`interest_tax`) | 1221.03 应收账款-清分 | 1221.02.02 应收账款-已到期-应收利息 | To B 清分结果 | 利息税（`interest_tax`）同步转出至应交税费。 |
| 2.2 清分-服务费 | `repay_before_compensate` (`amount_type=fin_service`/`tax_fin_service`) | 1221.03 应收账款-清分 | 1221.02.03 应收账款-已到期-应收服务费 | To B 清分结果 | -- |
| 2.2 清分-罚息 | `repay_before_compensate` (`amount_type=lateinterest`/`lateinterest_tax`) | 1221.03 应收账款-清分 | 1221.02.04 应收账款-已到期-应收罚息 | To B 清分结果 | `lateinterest_tax` 若不存在需在 To B 数据新增字段。 |
| 2.2 清分-滋缴金 | `overflow` | 1221.03 应收账款-清分 | 2241.05 其他应付款-滋缴金 | To B 清分结果 | -- |
| 2.2 实收利息转已实现收入 | `repay_before_compensate` (`amount_type=interest`) | 6001.02.01 营业收入-已到期收入-利息收入 | 6001.01.01 营业收入-已实现收入-利息收入 | To B 清分结果 | -- |
| 2.2 利息税转出 | `repay_before_compensate` (`amount_type=interest_tax`) | 6403.02 税金及附加-利息税 | 2221.04 应交税费-应交利息税 | To B 清分结果 | 与利息收入同步触发。 |
| 2.2 实收服务费转已实现收入 | `repay_before_compensate` (`amount_type=fin_service`) | 6001.02.02 营业收入-已到期收入-服务费收入 | 6001.01.02 营业收入-已实现收入-服务费收入 | To B 清分结果 | -- |
| 2.2 服务费 VAT 转出 | `repay_before_compensate` (`amount_type=tax_fin_service`) | 6001.01.02 营业收入-已实现收入-服务费收入 | 2221.01.04 应交税费-应交增值税-销项税额 | To B 清分结果 | 解决 VAT 与利息税去向。 |
| 2.2 实收罚息转已实现收入 | `repay_before_compensate` (`amount_type=lateinterest`) | 6001.02.03 营业收入-已到期收入-罚息收入 | 6001.01.03 营业收入-已实现收入-罚息收入 | To B 清分结果 | BI 罚息：以 `lateinterest` 识别，放款口径不再单独确认。 |
| 2.2 罚息税转出 | `repay_before_compensate` (`amount_type=lateinterest_tax`) | 6403.02 税金及附加-利息税 | 2221.04 应交税费-应交利息税 | To B 清分结果 | -- |
| 2.2 减免/优惠券-本金 | `loan_decrease` (`amount_type=principal`) | 6001.01.04 营业收入-已实现收入-减免/优惠券 | 1221.02.01 应收账款-已到期-应收本金 | To B 清分结果 | -- |
| 2.2 减免/优惠券-利息 | `loan_decrease` (`amount_type=interest`/`interest_tax`) | 6001.01.04 营业收入-已实现收入-减免/优惠券 | 1221.02.02 应收账款-已到期-应收利息 | To B 清分结果 | -- |
| 2.2 减免/优惠券-服务费 | `loan_decrease` (`amount_type=fin_service`/`tax_fin_service`) | 6001.01.04 营业收入-已实现收入-减免/优惠券 | 1221.02.03 应收账款-已到期-应收服务费 | To B 清分结果 | -- |
| 2.2 减免/优惠券-罚息 | `loan_decrease` (`amount_type=lateinterest`/`lateinterest_tax`) | 6001.01.04 营业收入-已实现收入-减免/优惠券 | 1221.02.04 应收账款-已到期-应收罚息 | To B 清分结果 | -- |

#### 14.2.3 特殊场景

| 场景 | `transType` | 借方科目 | 贷方科目 | 数据来源 | 备注 |
|------|-------------|----------|----------|----------|------|
| 2.2 特殊：提前还款重分类 | `repay_reclass_early` *(建议)* | 同 1.2.2 到期口径分录 | 相反方向 | 清分系统触发 | 当实际还款日 < 计划还款日时，由清分系统在拆分前补发此 `transType`，将未到期余额重分类到已到期。 |
| 2.3 Escrow 自动提现至资方 RDL | `escrow_auto_withdraw` | 1011.01.99 业务资金-BNI RDL-待处理 | 1011.02.99 业务资金-BNI 020-待处理 | Escrow 自动提现事件 | -- |
| 2.3 服务费/VAT/税金转运营资金 | `escrow_to_operation` | 2241.04 其他应付款-通道提现 | 1011.02.99 业务资金-BNI 020-待处理 | Escrow 自动提现事件 | 运营资金记账采用内部往来，避免与财务重复入账。 |
| 2.3 银行余额调整 | `bank_adjustment_escrow` | 1011.02.99 业务资金-BNI 020-待处理 | 1011.02.01 业务资金-BNI 020-银行余额 | 银行流水打标 | -- |
| 4 滋缴金退款 | `overflow_refund` | 2241.05 其他应付款-滋缴金 | 1011.02.99 业务资金-BNI 020-待处理 | 借款人退款指令 | -- |
| 4 银行余额调整 | `bank_adjustment_escrow` | 1011.02.99 业务资金-BNI 020-待处理 | 1011.02.01 业务资金-BNI 020-银行余额 | 银行流水打标 | -- |

### 14.3 审计与重生成要点补充

- **消息维度重放**：每个消息记录其 `transType` 与 `amount_type`，凭证聚合持久化消息哈希与规则版本，实现精准重放。
- **场景感知的冲销策略**：
  - 放款冲正（`loan_reverse`）由放款域发出，自动生成反向分录并写入原凭证的 `SupersededBy` 字段。
  - 优惠/减免类消息需保留原始事件，以便审计可区分“事后优惠”与“放款时优惠”。
- **VAT/利息税处理**：在收入确认阶段即生成税额凭证，并在支付环节（如税金缴纳）再次匹配 `transType=tax_payment` 的规则，确保税金生命周期闭环。
- **罚息策略**：罚息统一由清分系统根据逾期天数计算，以 `lateinterest`/`lateinterest_tax` 进入账务。放款口径不再确认罚息，避免重复入账。
- **拨备/滋缴金账户**：保留在通道体系内，不自动提现；后续若需提现，需新增独立 `transType` 并配套业务审批。

---

该架构设计强调清晰的领域边界、可扩展的规则匹配能力以及高可用、高一致性。通过 DDD 思想与 Go 语言实现，系统能够稳定地处理外部消息、生成凭证并提供再处理能力，满足业财一体化的核心诉求。
