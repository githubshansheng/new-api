# 限时额度回收数据库增量 DDL 说明

## 文件选择

| 数据库 | 文件 | 适用基线 |
| --- | --- | --- |
| MySQL 5.7.8+ | `limited-quota-reclaim.mysql.sql` | 未部署、已部署首版或部分部署限时额度回收字段 |
| PostgreSQL 9.6+ | `limited-quota-reclaim.postgresql.sql` | 未部署、已部署首版或部分部署限时额度回收字段 |
| SQLite（推荐） | `apply_limited_quota_reclaim_sqlite.sh` | 自动检查基线、备份、升级和验收，可重复执行 |
| SQLite | `limited-quota-reclaim.sqlite.sql` | `redemptions` 尚无 `reclaim_status` 的旧库 |
| SQLite | `limited-quota-reclaim-review.sqlite.sql` | 已有首版 6 个回收字段，但尚无 `reclaim_reviewed_by` |

MySQL 和 PostgreSQL 脚本会按字段、索引名称跳过已存在对象，可用于完整升级或仅补齐
人工核对字段。SQLite 不支持 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`，两个 SQL 文件
仍是一次性底层脚本；应通过 Shell 入口自动判断后调用，命令可重复执行。

## SQLite 推荐执行方式

SQLite 数据库本身没有用户名和密码。New API 仅在空数据库首次初始化时创建默认应用
管理员 `root / 123456`；已有系统的管理员密码可能已经修改。DDL 升级直接访问数据库
文件，不使用应用管理员账号，但 Shell 脚本会显示该默认信息，避免混淆。如果系统仍
使用默认密码，应在升级后立即登录并修改。

Shell 入口依赖 Bash 和 SQLite CLI，可先执行 `sqlite3 --version` 检查。停止连接同一
数据库的全部应用实例后，启动交互式 CLI：

```bash
bash patch/DDL/apply_limited_quota_reclaim_sqlite.sh
```

也可直接指定数据库，脚本识别结构后会显示“备份并升级 / 只检查 / 不备份升级 / 取消”
菜单：

```bash
bash patch/DDL/apply_limited_quota_reclaim_sqlite.sh "/实际路径/one-api.db"
```

自动化部署使用 `--yes` 跳过菜单，默认仍会先备份：

```bash
bash patch/DDL/apply_limited_quota_reclaim_sqlite.sh \
  --database "/实际路径/one-api.db" \
  --yes
```

只检查、不修改：

```bash
bash patch/DDL/apply_limited_quota_reclaim_sqlite.sh \
  --database "/实际路径/one-api.db" \
  --check-only
```

脚本行为：

- 无回收字段：先创建带 UTC 时间戳的数据库备份，再执行完整升级。
- 已有首版 6 个字段：先备份，再补充 3 个人工核对字段。
- 已有全部 9 个字段：校验字段和索引后成功跳过，不重复执行 DDL。
- 部分字段、字段约束错误或同名索引定义错误：拒绝自动修改并返回失败。

SQLite 可先执行以下查询选择脚本：

```sql
SELECT "name"
FROM pragma_table_info('redemptions')
WHERE "name" IN ('reclaim_status', 'reclaim_reviewed_by')
ORDER BY "name";
```

- 无结果：执行 `limited-quota-reclaim.sqlite.sql`。
- 只有 `reclaim_status`：执行 `limited-quota-reclaim-review.sqlite.sql`。
- 两个字段都有：无需执行本组 DDL。

## 结构变化

本次只修改 `redemptions`，不新增用户余额、消费统计或日志计费字段。

| 字段 | 类型约束 | 用途 |
| --- | --- | --- |
| `reclaim_status` | 整数，非空，默认 0 | 回收流程状态 |
| `reclaim_remaining_quota` | 整数，非空，默认 0 | 当前归属于兑换码的剩余限时额度 |
| `reclaimed_quota` | 整数，非空，默认 0 | 实际回收额度 |
| `reclaim_enabled_time` | bigint，非空，默认 0 | 启用回收时间（Unix 秒） |
| `reclaimed_time` | bigint，非空，默认 0 | 完成回收时间（Unix 秒） |
| `reclaim_error` | text，可空 | 自动重建或回收失败原因 |
| `reclaim_reviewed_by` | 整数，非空，默认 0 | 人工核对管理员 ID |
| `reclaim_reviewed_time` | bigint，非空，默认 0 | 人工核对时间（Unix 秒） |
| `reclaim_review_note` | text，可空 | 人工核对审计备注 |

新增组合索引：

- `idx_redemption_reclaim_due (reclaim_status, expired_time)`
- `idx_redemption_reclaim_user (used_user_id, reclaim_status)`
- `idx_redemption_reclaim_completed (reclaim_status, reclaimed_time)`

历史记录的数值字段统一补为 `0`，文本字段统一补为空字符串。`reclaim_status=0`
表示未启用，执行 DDL 不会自动把历史兑换码加入回收流程；仍需管理员粘贴兑换码并确认。

## 执行建议

1. 备份主数据库。
2. 停止连接同一主数据库的全部应用写入节点。
3. 根据数据库类型和上述基线选择执行脚本。
4. 验证 9 个字段、3 个索引以及历史数据无 `NULL` 后再启动应用。

MySQL DDL 会自动提交；PostgreSQL 和 SQLite 脚本使用事务。应用自身迁移也会补齐这些
字段，但生产环境使用本组 DDL 可在部署前完成可控的结构升级。

本组脚本不提供自动降级。回退旧版本应用时应保留新增字段；如必须移除，应在所有写入
停止后通过完整数据库备份恢复，避免丢失回收状态和人工审计信息。
