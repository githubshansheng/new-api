# 链动支付数据库 DDL 说明

## 文件选择

| 数据库 | 文件 | 适用场景 |
| --- | --- | --- |
| MySQL 5.7.8+ | `liandong-payment.mysql.sql` | 全新建表，或从 2026-07-15 初版链动表升级 |
| PostgreSQL 9.6+ | `liandong-payment.postgresql.sql` | 全新建表，或从 2026-07-15 初版链动表升级 |
| SQLite | `liandong-payment.sqlite.sql` | 仅用于从 2026-07-15 初版链动表执行一次升级 |
| SQLite | `liandong-payment.sqlite-fresh.sql` | 仅用于数据库中不存在任何链动表的全新建表 |

生产环境必须通过 Patch 包中的 `patchctl.sh` 和 `patchdb` 执行迁移，不应直接运行
SQL 文件。`patchdb` 会先识别数据库类型、链动功能开关和当前结构，再选择对应脚本。

SQLite 不支持可靠的条件加列和修改列约束，升级脚本通过事务重建
`liandong_products`、`liandong_orders`。脚本内置旧结构保护，仅接受精确的
2026-07-15 初版列和索引签名；目标结构、部分迁移、缺表或自定义扩展字段都会中止，
防止重复执行时重置业务值或删除扩展字段。

## 本次结构变化

### `liandong_products`

新增字段：

| 字段 | 用途 |
| --- | --- |
| `goods_type` | 保存链动商品类型，用于商品查询、筛选和快捷填充 |
| `inventory_mode` | 区分无限库存和 new-api 内部兑换码库存 |
| `inventory_capacity` | 库存容量及补货上限 |
| `thumbnail_version` | 商品缩略图版本，用于客户端缓存失效 |

历史商品补为 `goods_type='card'`、`inventory_mode='unlimited'`、
`inventory_capacity=0`、`thumbnail_version=0`。

### `liandong_orders`

新增字段：

| 字段 | 用途 |
| --- | --- |
| `inventory_code_id` | 记录订单预占的内部库存码 |
| `expires_at` | 服务端支付超时时间 |
| `closed_reason` | 用户关闭、超时和创建失败等关闭原因 |
| `late_payment` | 标记库存释放后发现的迟付订单 |

历史订单以上字段补为 `0`、空字符串或 `false`。旧字段
`check_deadline_at` 为兼容历史数据继续保留。

### 新表

| 表 | 用途 |
| --- | --- |
| `liandong_product_thumbnails` | 独立保存裁剪后的商品缩略图 |
| `liandong_product_inventory_codes` | 保存内部库存码及可用、预占、消费、停用状态 |
| `liandong_user_operation_leases` | 为同一用户的创建订单和换单提供跨实例短租约 |

## 未发生 DDL 变化的部分

- `top_ups` 和 `subscription_orders` 已有 `payment_provider` 字段，本次仅新增
  `liandong` 业务值。
- JUUID、账号、密码、merchant-token、核验间隔、批次大小和支付超时继续保存在
  `options` 表，不新增敏感配置表，DDL 中也不写入凭据。
- 后台核验复用 `system_tasks`、`system_task_locks`，仅新增任务类型值。
- 金额继续以 `expected_amount_minor` 整数分保存。

## 执行与回退

1. 关闭所有链动功能开关。
2. 停止共享主数据库对应的全部应用写入节点。
3. 通过 `patchctl.sh deploy --migration-leader --confirm-writers-stopped` 在首节点执行。
4. 验证五张链动表、字段、约束和索引后，再部署其余节点。
5. 所有节点完成后才能重新开启链动功能。

MySQL DDL 会自动提交；Patch 工具会在执行前备份数据库，并在每一步后验收结构。
PostgreSQL DDL 与验收由 `patchdb` 放在同一事务中，SQLite 脚本使用
`BEGIN IMMEDIATE`。验收不仅检查表和字段名称，还检查本次新增字段类型、字符串长度、
NOT NULL 契约，以及三张新表的完整字段类型、可空性和索引定义。已存在但契约不一致
的新表会被判定为 `unsafe`，不会继续迁移。

`patchdb` 使用 `new_api_patch_history` 记录 Patch ID、DDL bundle SHA-256、
`dirty/success` 状态和时间。MySQL 中断后只有结构仍符合可续跑契约且 DDL 校验和未变
时才允许继续；成功记录与真实结构不一致或 DDL 被替换时会拒绝迁移。

Patch 不提供自动降级 SQL。正常回退只恢复旧二进制和 `new-api.sh`，保留新增字段和
表，避免覆盖部署后产生的新订单、库存及履约数据。只有在全部节点停止并明确接受数据
覆盖风险时，才应人工恢复完整数据库备份。
