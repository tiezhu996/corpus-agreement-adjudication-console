# 代码结构地图：corpus-agreement-adjudication-console / backend

- 模块：`corpus-annotation-agreement-control/backend`（Gin + GORM + JWT，Postgres/SQLite）
- 统计：54 个 .go 文件，约 4552 行；service/handler/repository 三层 + middleware/dto/model/constants/algorithm/matching/router/cmd
- 包依赖方向：handler → service → repository(+system 审计)；service 持有 `*gorm.DB` 并用 `WithDB(tx)` 在事务内换库
- 阅读日期：2026-08-25（只读走读，未修改任何源码）

---

## 1. 逐文件函数地图（签名 + 行号 + 职责）

### cmd/server/main.go（57 行）
- `main()` L16-57：加载配置、开库(含 seed)、启动 http.Server（ReadHeaderTimeout 5s/ReadTimeout 20s/WriteTimeout 45s/IdleTimeout 60s）、监听 SIGINT/SIGTERM 后 10s 优雅关闭。

### internal/config/config.go（281 行）
- `type Config` L27-38：端口/驱动/DSN/迁移/JWT/限流/算法版本等配置结构。
- `Load()` L40-66：读环境变量并校验（JWT_SECRET≥24 字节、限流 30~10000/min、TTL 5min~24h、算法版本非空）。
- `OpenDatabase(cfg)` L68-98：选 postgres/sqlite 拨号器、GORM 日志、AutoMigrate 6 张表、调 `seed`。
- `seed(db,cfg)` L100-237：幂等播种 7 个账号(bcrypt)、1 个 frozen 数据集、1 个 published 规范、2 个 compared 标注集、1 个 open 裁决案件（用 `algorithm.Compute`+`matching.CompareAnnotations` 生成快照）。
- `ratingMap(labels)` L239-249：把标签转成 `unit_key[:start-end] -> label` 的评分 map（与 service.ratingSet 逻辑重复）。
- `digest(value)` L251-254：sha256 hex。
- `envString/envInt/envBool` L256-281：环境变量读取，解析失败静默回退默认值。

### internal/constants/annotation_state.go（83 行）
- const 块 L3-32：5 角色 + 数据集 3 态 + 规范 4 态 + 标注集 6 态 + 案件 6 态。
- `ValidRole` L34-41 / `ValidAnnotationState` L43-50：合法性判断。
- `CanTransitionDataset(from,to)` L52-55：draft→frozen→archived。
- `CanTransitionSchema` L57-61：draft→validated→published→deprecated。
- `CanTransitionAnnotation` L63-72：draft→submitted→{locked,returned}；returned→draft；locked→compared；compared→superseded。
- `CanTransitionCase` L74-83：open|reopened→assigned→adjudicated→reviewed→{accepted,reopened}。

### internal/constants/disagreement_type.go（17 行）
- const L3-8 + `ValidDisagreementType` L10-17：boundary/label/omission/overlap 四种分歧类型。

### internal/dto/*.go（纯类型 + 校验 tag）
- `adjudication_case.go`：`AgreementDetails` L5、`ConfusionCell` L16、`DiffEvidence` L22、`ComputeAdjudicationRequest` L35（dataset_id/item_key/annotation_set_ids min=2/metric oneof）、`AssignCaseRequest` L42、`AdjudicateCaseRequest` L46（final_labels min=1、rationale min=12）、`CaseReviewRequest` L51、`AdjudicationCaseResponse` L55（含 Reused bool）。
- `annotation_schema.go`：`LabelDefinition` L8、`MaskedExample` L15、`CreateAnnotationSchemaRequest` L21、`UpdateAnnotationSchemaRequest` L31、`CopyAnnotationSchemaRequest` L39、`SchemaTransitionRequest` L43、`AnnotationSchemaResponse` L47。
- `annotation_set.go`：`AnnotationLabel` L5、`IsSpan()` L12（`End > Start` 判定 span）、`CreateAnnotationSetRequest` L14（source_checksum len=64 hex）、`UpdateAnnotationSetRequest` L24、`AnnotationTransitionRequest` L29、`AnnotationSetResponse` L34。
- `corpus_dataset.go`：`CreateCorpusDatasetRequest` L5、`UpdateCorpusDatasetRequest` L15（带 version 乐观锁字段）、`CorpusDatasetResponse` L25（含 schema_count/annotation_count）。
- `system.go`：`LoginRequest` L5、`UserView` L10、`LoginResponse` L16、`Actor` L22、`PageMeta` L28、`AuditEventResponse` L35。

### internal/model/*.go（GORM 模型，均无 BeforeSave/AfterFind 钩子）
- `adjudication_case.go` L5-37：`AdjudicationCase`，`IdempotencyKey` 与 `DecisionIdempotencyKey` 为 uniqueIndex（后者 `*string`），`ReopenCount` 默认 0，`AdjudicatorID/ReviewedBy/DecidedAt` 可空，关联 Dataset。
- `annotation_schema.go` L5-20：`(DatasetID,SchemaCode,Version)` 唯一复合索引，SchemaState 有索引。
- `annotation_set.go` L5-22：`(DatasetID,SchemaID,AnnotatorID,ItemKey,SourceChecksum)` 唯一索引，SupersedesID 可空。
- `corpus_dataset.go` L5-19：DatasetCode 唯一，DatasetState/Version 有索引。
- `system.go` L5-28：`User`（Username 唯一、Active）、`AuditEvent`（Before/After/Parameters 三列 JSON 文本）。

### internal/repository/*.go（全部 repo 为无状态结构体，`WithDB(tx)` 返回新实例；错误统一 `fmt.Errorf("...: %w", err)` 包装；写操作全部用 `Updates(map)` + `RowsAffected==1` 判定冲突）

**adjudication_case.go（129 行）**
- `NewAdjudicationCaseRepository` L14 / `WithDB` L18：构造/换事务库。
- `Create(adjudication)` L22-27：插入案件。
- `Get(id)` L29-35：Preload("Dataset") 取单条。
- `List(page,pageSize,datasetID,state,disagreementType,clusterKey)` L37-61：动态过滤 + Count + Offset/Limit 分页，created_at DESC,id DESC。
- `FindByIdempotencyKey(key)` L63-69：幂等键查案件（含 Dataset）。
- `LatestByInput(inputHash,algorithmVersion)` L71-79：同输入哈希+算法版本取最新一条（id DESC）。
- `Assign(id,actorID)` L81-92：`WHERE id AND case_state IN ('open','reopened')` → 置 assigned/adjudicator_id；RowsAffected!=1 返 `ErrStateConflict`。
- `Decide(id,actorID,labelsJSON,rationale,idempotencyKey)` L94-110：`WHERE id AND case_state='assigned' AND adjudicator_id=actor` → 置 adjudicated/final_labels/rationale/decided_at/decision_idempotency_key；RowsAffected!=1 返 `ErrStateConflict`。
- `Review(id,from,to,reviewerID,rationale)` L112-128：`WHERE id AND case_state=from` → 置 to/reviewed_by；rationale 用 `gorm.Expr("rationale || ?")` 追加；to=reopened 时 `reopen_count+1` 且 `adjudicator_id=nil`。

**annotation_schema.go（99 行）**
- `Create` L22-27 / `Get` L29-35（Preload Dataset）/ `List` L37-58（dataset_id/schema_state/schema_code 过滤，dataset_code,version DESC 排序）。
- `PublishedForDataset(datasetID)` L60-67：取某数据集所有 published 规范（未被 handler/service 调用，疑似预留）。
- `UpdateDraft(schema,expectedUpdatedAt)` L69-84：`WHERE id AND version AND schema_state='draft' AND updated_at=?` → 更新四列；RowsAffected!=1 返 `ErrVersionConflict`。
- `Transition(id,from,to)` L86-98：`WHERE id AND schema_state=from` → 置 to；to=published 时写 published_at。

**annotation_set.go（95 行）**
- `Create` L22-27 / `Get` L29-35（Preload Dataset/Schema/Annotator）。
- `ByIDs(ids)` L37-44：`id IN ?` 批量取（id ASC，供 compute 用）。
- `List` L46-67：dataset_id/item_key/annotation_state 过滤，item_key ASC,id DESC 分页。
- `UpdateDraft(id,annotatorID,expectedUpdatedAt,labelsJSON,qualityNote)` L69-80：`WHERE id AND annotator_id AND annotation_state='draft' AND updated_at=?`；RowsAffected!=1 返 `ErrStateConflict`。
- `Transition(id,from,to)` L82-94：`WHERE id AND annotation_state=from` → 置 to；to=submitted 写 submitted_at。

**corpus_dataset.go（97 行）**
- `Create` L21-26 / `Get` L28-34 / `List` L36-56（state/language/owner_team 过滤，dataset_code ASC）。
- `Update(dataset,expectedVersion)` L58-73：`WHERE id AND version AND dataset_state='draft'` → 更新内容列并把 `version=expectedVersion+1`；RowsAffected!=1 返 `ErrVersionConflict`。
- `Transition(id,expectedVersion,from,to)` L75-86：`WHERE id AND version AND dataset_state=from` → 置 to + `version+1`。
- `Counts(id)` L88-97：统计该数据集的 schema 数与 annotation 数（两个独立 COUNT，非事务）。

**system.go（79 行）**
- `ErrVersionConflict/ErrStateConflict` L14-17：乐观锁/状态冲突哨兵错误。
- `IsUniqueViolation(err)` L19-25：字符串包含 unique constraint/duplicate key/unique failed 判定唯一冲突（弱判定，靠错误文本）。
- `NewSystemRepository` L29 / `WithDB` L30-32 / `DB()` L33。
- `FindUser(username)` L35-41：按用户名+active 查用户（登录用）。
- `CreateAudit(event)` L43-48：写审计事件。
- `ListAudit(page,pageSize,actor,requestID,resourceType,action,from,to)` L50-79：多条件过滤 + 时间范围 + 分页（created_at DESC,id DESC）。

### internal/service/*.go

**system.go（231 行）— 错误类型 + 审计 + 认证**
- `type AppError` L21-26：{Status,Code,Message,Cause} 统一错误结构。
- `(*AppError).Error()` L28-33 / `Unwrap()` L34：含 cause 的文本与 errors.Is/As 链。
- `BadRequest` L36 / `Unprocessable` L39 / `Unauthorized` L42 / `Forbidden` L45 / `NotFound` L48 / `Conflict` L51 / `Internal` L54：AppError 工厂。
- `firstCause` L57-62。
- `MapRepositoryError(resource,err)` L64-69：**错误映射核心**——`gorm.ErrRecordNotFound`（或文本含 record not found）→ NotFound(404)，其余一律 Internal(500)。
- `type Claims` L71-76 / `NewSystemService` L84-86。
- `Login(request)` L88-107：FindUser + bcrypt 比对（失败统一 Unauthorized，不区分用户不存在/密码错），签发 HS256 JWT（iss=corpus-agreement-api）。
- `ParseToken(encoded)` L109-121：校验签名方法 HS256 + issuer + 过期，返回 dto.Actor；**无角色吊销/Active 复查**。
- `RecordAudit` L123-125 / `RecordAuditTx(db,...)` L127-129：审计入口（事务版用 WithDB(tx)）。
- `record(repo,...)` L131-141：组装 AuditEvent 并写入，失败返 Internal（**在事务内会触发整体回滚**）。
- `ListAudit` L143-158：分页 + decodeSummary。
- `PageMeta` L160-162：`ceil(total/pageSize)` 计算总页数（pageSize=0 会 panic? 不会——handler 保证≥1）。
- `encodeSummary` L164-181：JSON 序列化→反序列化→`redactAuditValue` 脱敏→再序列化；失败返回 `{"summary":"unavailable"}`。
- `decodeSummary` L183-192：解析失败返回 `{"summary":"unavailable"}`。
- `auditID` L194 / `redactAuditValue` L196-217：递归脱敏 map/数组；`sensitiveAuditKey` L219-231：敏感键判定（labels/examples/password/token/secret/credential 及 rawtext/originalcontent 后缀）。

**corpus_dataset.go（162 行）**
- `datasetCodePattern` L16：`^[A-Z0-9][A-Z0-9_-]{2,63}$`。
- `Create(request,actor,requestID)` L28-53：校验 code 格式 → 构造 draft v1 → 事务创建（唯一冲突→Conflict duplicate_dataset_code）+ 审计。
- `Get(id)` L55-61 / `List` L63-77（每行调 response 聚合计数，单条失败即整体失败）。
- `Update(id,request,actor,requestID)` L79-112：先 Get 校验 → 事务内 `Update(expectedVersion)`（ErrVersionConflict→Conflict version_conflict）+ 审计。
- `Transition(id,target,expectedVersion,actor,requestID)` L114-141：`CanTransitionDataset` 预检 → 事务内条件更新 + 审计。
- `response(dataset)` L143-154：查 schema/annotation 计数拼响应。
- `datasetSummary` L156-162：审计摘要。

**annotation_schema.go（219 行）**
- `Create(request,actor,requestID)` L27-60：校验标签定义/示例 → **Get dataset 并拒绝 archived**（L35-37）→ 构造 draft → 事务创建（唯一冲突→duplicate_schema_version）+ 审计。
- `Get` L62-68 / `List` L70-80。
- `Update(id,request,actor,requestID)` L82-119：先校验 `before.Version==request.Version`（版本预检）→ 事务内 `UpdateDraft`（ErrVersionConflict→Conflict version_conflict）+ 审计。
- `Copy(id,request,actor,requestID)` L121-146：取源 schema（**不检查数据集状态**）→ 复制内容为新 draft 版本 → 事务创建 + 审计。
- `Transition(id,target,actor,requestID)` L148-175：`CanTransitionSchema` 预检 → 事务内条件更新 + 审计。
- `validateSchemaDefinitions` L177-195：label code 去重、task_type 合法、示例必须含 `***` 或 `[MASK]`。
- `schemaResponse` L197-209 / `schemaSummary` L211-219。

**annotation_set.go（246 行）**
- `Create(request,actor,requestID)` L28-87：**要求数据集 frozen（L33-35）且 schema published 且属于该数据集（L40-42）** → 标签规范化 → 事务内：若 SupersedesID 非空则校验"本人+compared+同数据集/同 item"（L56-67）→ 创建 draft（唯一冲突→duplicate_annotation_revision）→ 把被取代的 compared 标注转 superseded（L74-78）→ 审计。
- `Get` L89-95 / `List` L97-107。
- `Update(id,request,actor,requestID)` L109-143：仅本人 draft 可改 → `UpdateDraft`（ErrStateConflict→Conflict state_conflict）+ 审计。
- `Transition(id,request,actor,requestID)` L145-171：`CanTransitionAnnotation` 预检 → `authorizeAnnotationTransition` 权限预检 → 事务内条件更新 + 审计。
- `authorizeAnnotationTransition` L173-184：submit/draft 仅本人；lock/returned/compared/superseded 需 data_manager/adjudicator/admin。
- `normalizeAndValidateLabels` L186-219：**标签规范化核心**——按 schema 定义校验 label 存在、span 需 End>Start、classification 需 Start=End=0 且同 unit_key 去重。
- `annotationResponse` L221-232 / `labelCount` L234-238 / `annotationSummary` L240-246。

**adjudication_case.go（503 行）— 最复杂业务**
- `NewAdjudicationCaseService` L30-35：注入 db、案件 repo、标注 repo、system、算法版本。
- `Compute(request,idempotencyKey,actor,requestID)` L37-129：**裁决案件生成**——①ByIDs 取标注并校验数量=去重后数量；②`validateComparableAnnotations`；③算 inputHash；④幂等键查重（不同输入→Conflict idempotency_conflict，相同→Reused）；⑤`LatestByInput` 复用（相同输入直接返回 Reused）；⑥`algorithm.Compute` + `compareAll` + `summarizeEvidence`；⑦无分歧→Unprocessable no_disagreement；⑧事务：建案件（唯一冲突兜底）+ 把 locked 标注转 compared（L95-127）+ 审计；⑨成功后 Get 返回。
- `Get` L130-137 / `List` L138-149。
- `Assign(id,request,actor,requestID)` L150-184：先 Get 预检 open/reopened + **拥有权预检（本人标注不可认领，L159-165 在事务外）** → 事务内 Assign + 审计。
- `Decide(id,request,idempotencyKey,actor,requestID)` L185-242：先 Get → 幂等键预检（已有同键→Reused；不同键→Conflict）→ 预检 assigned+本人+拥有权 → `loadCaseAnnotations` → `normalizeAndValidateLabels(...,annotations[0].Schema)` → 事务内 Decide 条件更新 + 审计；失败后重载比对 `decision_idempotency_key` 判重（L226-233）。
- `Review` L243-260：预检 adjudicator 不能审自己的案件、拥有权 → `caseTransition(CaseReviewed)`。
- `Accept` L261-271：预检 `ReviewedBy==actor` → caseTransition(CaseAccepted)。
- `Reopen` L272-282：预检 `ReviewedBy==actor` → caseTransition(CaseReopened)。
- `caseTransition(before,target,note,actor,requestID)` L283-306：`CanTransitionCase` 预检 → 事务内 Review 条件更新 + 重载 + 审计。
- `validateComparableAnnotations` L308-341：≥2 标注、同 dataset+item、同 schema、状态必须 locked/compared、≥2 个不同标注者；解析 LabelsJSON。
- `ratingSet` L342-353：标签→评分 map（与 config.ratingMap 重复）。
- `compareAll` L354-376：以第一个标注集为基准两两 CompareAnnotations，汇总 evidence 与 confusion（"\x00" 拼接键）。
- `summarizeEvidence` L378-409：按 priority(overlap>omission>label>boundary) 选主导分歧类型 + 最频繁 label pair。
- `adjudicationInputHash` L411-419：**对 annotations 切片原地 sort.Slice** 后拼 dataset_id/item_key/metric/version + 各标注 id:checksum:labels 哈希。
- `adjudicationResponse` L421-447：JSON 快照反序列化（**错误全部忽略**）→ DTO。
- `ownsAnyAnnotation` L449-460 / `loadCaseAnnotations` L462-478：解码 AnnotationSetIDsJSON 并 ByIDs 校验完整性（**空 ids 报错**）。
- `caseSummary` L480-489 / `uniqueIDs` L491-497 / `sortedIDs` L499-503（先复制再排序）。

### internal/handler/*.go（薄壳：BindAndValidate → service → WriteData/WritePage/WriteError）

**system.go（162 行）— 公共工具也在这**
- `ActorContextKey` L19、`validate` L21（全局 validator 实例）。
- `SystemHandler` L23-30、`Login` L32-44、`Health` L46-48、`Ready` L50-61（**唯一使用 context.Request.Context() 的地方**：PingContext）、`Audit` L63-81（from/to 时间过滤）。
- `BindAndValidate` L83-91：ShouldBindJSON + validator.Struct → BadRequest invalid_json/validation_error。
- `WriteData` L93-95 / `WritePage` L97-99 / `WriteError` L101-110（非 AppError → Internal 500；≥500 记日志）。
- `Actor(context)` L112-115：`context.MustGet(...).(dto.Actor)` —— **MustGet 无 key 时 panic**（依赖中间件顺序）。
- `RequestID` L117 / `Pagination` L119-132（page<1→1，pageSize 1~200，**溢出风险见 §6**）/ `PathID` L134-140（ParseUint 32 位 + 非 0）/ `QueryUint` L142-145（解析失败静默 0）/ `optionalTime` L147-153 / `validationMessage` L155-162。

**corpus_dataset.go（97 行）**：`List` L18、`Get` L30、`Create` L44、`Update` L58、`Transition` L77-97（内联请求体 target_state oneof=frozen/archived + version）。
**annotation_schema.go（115 行）**：`List` L20、`Get` L32、`Create` L46、`Update` L60、`Copy` L79、`Transition` L98。
**annotation_set.go（94 行）**：`List` L18、`Get` L30、`Create` L44、`Update` L58、`Transition` L77。
**adjudication_case.go（132 行）**：`List` L20、`Get` L33、`Compute` L47-63（读 Idempotency-Key 头，reused→200 否则 201）、`Assign` L65、`Decide` L84-101（reused 布尔被丢弃，恒 200）、`Review` L103 / `Accept` L107 / `Reopen` L111（复用 `reviewAction` L115-132 泛型化包装）。

### internal/middleware/*.go
- `audit.go` `Audit()` L10-23：请求日志（request_id/method/route/status/latency/ip/bytes），route 为空记 "unmatched"。
- `auth.go` `Auth(system)` L13-28：校验 `Authorization: Bearer` 前缀 + ParseToken，注入 `authenticated_actor`；`writeAuthError` L30-35（**用类型断言 `err.(*AppError)` 而非 errors.As**，包装错误会退化成通用 Unauthorized）。
- `error_handler.go` `ErrorHandler()` L10-25：加安全头，`context.Errors` 非空且未写响应时兜底 500（只处理 c.Error 注入的错误）。
- `rbac.go` `RBAC(roles...)` L10-27：取 actor 校验角色白名单；缺失上下文→401，角色不符→403。
- `recovery.go` `Recovery()` L11-23：recover panic → 500 + 记 stack。
- `request_id.go`：`RequestID()` L14-24（透传/生成 X-Request-ID，>80 字符重生成）；`CORS(origin)` L26-41（仅精确匹配 Origin，OPTIONS 直接 204）；`type rateBucket` L43-46；`RateLimit(limit,scope)` L48-77：**进程内 map+mutex 滑动分钟桶**（key=scope:clientIP，>4096 时清理 minute<current-1 的桶，超限 429 + Retry-After:60）；`newRequestID` L79-85（crypto/rand 失败回退时间戳）。

### internal/router/*.go
- `router.go` `type handlers` L17-24、`New(db,cfg)` L26-46：全局中间件链 `RequestID→ErrorHandler→Recovery→CORS→RateLimit(300/min,global)→Audit`；挂 `/healthz`、`/readyz`、`/api/v1/auth/login`（30/min）；`/api/v1` 下 `Auth` 保护 + 4 组业务路由 + `/audit`；NoRoute→404。`wire` L48-64：组装 repo→service→handler。
- `corpus_dataset.go` L11-19 / `annotation_schema.go` L11-20 / `annotation_set.go` L11-21 / `adjudication_case.go` L11-23：见 §8 路由表。

### internal/algorithm/agreement.go（196 行）— 一致性算法（纯函数）
- `RatingSet` L11-14。
- `ChooseMetric` L16-21：恰好 2 个且无缺失 → cohen_kappa，否则 krippendorff_alpha。
- `CohenKappa(left,right)` L23-70：两标注者按并集键计算 observed/chance/kappa，缺失单元跳过并计数。
- `KrippendorffAlpha(sets)` L72-133：多标注者名义 alpha；逐单元收集评分，两两比较算 observed 分歧，`expectedAgreement=Σ count*(count-1)/(total*(total-1))`。
- `Compute(metric,sets)` L135-151：auto 选择 + 分发；cohen 要求恰好 2 个标注集。
- `hasMissingRatings` L153-163 / `unionUnitKeys` L165-178（去重+排序）/ `normalizedAgreement` L180-188（chance≈1 特判）/ `clamp` L190-192（[-1,1]）/ `round6` L194-196。

### internal/matching/span.go（224 行）— 差异比对（纯函数）
- `CompareAnnotations(left,right)` L12-101：分类标签按 unit_key 比对（label/omission），span 用**贪心匹配**（`usedRight` 布尔数组 + 最大 overlap + exactBoundary 优先）产出 evidence/confusion，最后排序 + `dominantDisagreement`。
- `ClusterKey` L103-111：`schemaCode:type:pair` 小写拼接，空值兜底。
- `classificationMap` L113-121 / `spanLabels` L123-131 / `unionKeys` L133-147 / `overlapLength` L149-153（`max(0,min(end)-max(start))`）/ `exactBoundary` L155-157 / `spanEvidence` L159-165 / `omissionEvidence` L167-178。
- `dominantDisagreement` L180-215：与 service.summarizeEvidence 重复的计数逻辑（overlap>omission>label>boundary 优先级 + 最频繁 pair）。
- `confusionKey` L217（"\x00" 拼接）/ `valueOrMissing` L219-224（缺失→"∅"）。

---

## 2. 状态机与迁移表

| 实体 | 状态 | 合法迁移（constants 层） | repository 条件更新（乐观锁） |
|---|---|---|---|
| 数据集 | draft/frozen/archived | draft→frozen；frozen→archived | `Transition` L75-86：`WHERE id AND version=? AND dataset_state=from` → to + version+1；`Update` L58-73 仅 draft 且 version 匹配 |
| 规范 | draft/validated/published/deprecated | draft→validated→published→deprecated | `Transition` L86-98：`WHERE id AND schema_state=from`；to=published 写 published_at；`UpdateDraft` L69-84 仅 draft 且 version+updated_at 匹配 |
| 标注集 | draft/submitted/returned/locked/compared/superseded | draft→submitted；submitted→{locked,returned}；returned→draft；locked→compared；compared→superseded | `Transition` L82-94：`WHERE id AND annotation_state=from`；to=submitted 写 submitted_at；`UpdateDraft` L69-80 仅本人 draft 且 updated_at 匹配 |
| 裁决案件 | open/assigned/adjudicated/reviewed/accepted/reopened | open\|reopened→assigned→adjudicated→reviewed→{accepted,reopened} | `Assign` L81-92：`WHERE id AND case_state IN (open,reopened)`；`Decide` L94-110：`WHERE id AND case_state=assigned AND adjudicator_id=?`；`Review` L112-128：`WHERE id AND case_state=from`，reopen 时 `reopen_count+1` 且清 adjudicator_id |

校验逻辑：service 层在事务外先 `constants.CanTransitionXxx` 预检（返回 409 invalid_*_transition），事务内 repository 条件更新失败（RowsAffected!=1）再返回 409 state_conflict/version_conflict——**两层防御**。标注集另有 `authorizeAnnotationTransition`（owner/角色）预检；案件另有 adjudicator/reviewer/拥有权预检（事务外，TOCTOU）。

---

## 3. 错误处理模式

- **统一错误**：`service.AppError{Status,Code,Message,Cause}`，工厂函数 BadRequest/Unprocessable/Unauthorized/Forbidden/NotFound/Conflict/Internal；`Error()` 含 cause 文本，`Unwrap()` 支持 errors.Is/As。
- **repository → service 映射**：`MapRepositoryError`（system.go L64-69）——`errors.Is(gorm.ErrRecordNotFound)` 或错误文本含 "record not found" → NotFound；**其余一律 Internal("database operation failed") → 500**（包括唯一冲突等，不会被映射成 409）。
- **唯一冲突**：`repository.IsUniqueViolation` 靠**错误文本子串**匹配（unique constraint/duplicate key/unique failed），在事务内捕获后转 409 Conflict（duplicate_dataset_code / duplicate_schema_version / duplicate_annotation_revision / idempotency_conflict）。
- **乐观锁冲突**：`ErrVersionConflict`（version 变了）与 `ErrStateConflict`（state 变了），service 用 `errors.Is` 判定后转 409。
- **吞错点**：①`json.Marshal` 错误几乎全被忽略（schema/annotation/case 多处 `_, _ =`）；②`adjudicationResponse` 4 个 `json.Unmarshal` 错误全忽略（DB 脏 JSON → 静默空数组）；③`QueryUint`/`Pagination`/`envInt`/`envBool` 静默兜底；④main.go 忽略 `sqlDB.Close()` 错误；⑤`encodeSummary` 失败返回 `{"summary":"unavailable"}`。
- **500 常见来源**：List 的 repository 错误、`could not check computation idempotency`、`annotation labels could not be decoded`、`case annotation ownership could not be verified`、reload 失败、审计写入失败（事务内→回滚）、`WriteError` 兜底（非 AppError）。
- **审计错误**：RecordAudit 失败返回 Internal 并随事务回滚（有测试覆盖）。

---

## 4. 并发相关

- **事务**：所有写路径 `service.db.Transaction`（共 14 处：adjudication 4 / annotation_set 3 / annotation_schema 4 / corpus_dataset 3），repo 用 `WithDB(tx)` 换事务句柄；审计与业务同事务。
- **条件更新（乐观锁）**：全部写操作 `Updates(map)` + `RowsAffected==1` 校验，WHERE 带状态（和/或 version / updated_at / annotator_id / adjudicator_id）。
- **幂等键**：`idempotency_key`（计算案件）与 `decision_idempotency_key`（裁决）都是 uniqueIndex；service 先查后写，事务内唯一冲突兜底 → 重查返回 Reused=true（Compute L116-124、Decide L226-233）。**并发窗口**：两个相同幂等键请求同时通过预检，靠 DB 唯一索引兜底。
- **TOCTOU**：Assign/Decide/Review 的**拥有权预检在事务外**（先 Get 再事务更新）；状态一致性最终靠事务内 WHERE 条件保证，但拥有权/角色预检是"先读后写"窗口。
- **时间戳乐观锁**：schema/annotation 的 `UpdateDraft` 用 `updated_at = ?` 精确相等比对——GORM 时间精度/时区差异可能导致本应成功的更新被判冲突（或相反）。
- **RateLimit**：进程内 `map[string]rateBucket` + `sync.Mutex`（request_id.go L48-77）；桶清理仅当 `len(buckets)>4096`；多实例部署时各实例独立计数（不是分布式限流）。
- **竞态可能点**：①Compute 的 LatestByInput 复用（读-算-写非原子，重复计算窗口）；②标注 supersede 流程（Create 内先查旧标注再转 superseded，同事务所以 OK）；③`config.seed` 幂等（count>0 跳过，多实例启动可能双写用户——唯一索引兜底）。

---

## 5. slice/数组处理

- **复制后排序（安全）**：`sortedIDs`（adjudication_case.go L499-503）`append([]uint{}, values...)`。
- **原地排序（污染风险）**：`adjudicationInputHash`（L411-419）直接 `sort.Slice(annotations,...)` 改调用方切片（当前调用链上 ByIDs 已按 id 升序，暂无实际危害，但改变了入参顺序）。
- **预分配**：`unionUnitKeys`（cap=len(unique)）、`KrippendorffAlpha.values`（cap=len(sets)）、`normalizeAndValidateLabels`（cap=len(labels)）、List 响应（cap=len(rows)）、`compareAll` 的 confusion（cap=len(counts)）。
- **无 cap append**：`compareAll` 的 `allEvidence := make([]DiffEvidence,0)` 逐段 append；`matching.spanLabels` `make([]AnnotationLabel,0)`。
- **`[:0]` 复用**：未使用（无复用 buffer 模式）。
- **布尔标记数组**：`matching.CompareAnnotations` 的 `usedRight := make([]bool, len(rightSpans))` 贪心匹配去重。
- **排序**：evidence 按 UnitKey+LeftStart 稳定排序（span.go L82-87）；confusion 按 label 对排序；unionKeys 排序保证算法确定性。
- **边界**：`overlapLength` 用 `max(0,end-start)` 防负；span 需 `End>Start`（IsSpan/dto 校验）；classification 需 `Start=End=0`；`PathID` 拒绝 0；`Pagination` 限 pageSize≤200 但 **page 无上限 → Offset 整数溢出**（见 §6/§10-5）。

---

## 6. nil/panic 风险点

- **`handler.Actor`（system.go L112-115）**：`context.MustGet(ActorContextKey)` 在 key 缺失时 **panic**——依赖 Auth 中间件必须先于所有使用 Actor 的路由；login/health/ready 不经过 Auth 但也不调 Actor，故当前安全，属脆弱点。
- **`Decide` 的 `annotations[0]`（adjudication_case.go L215）**：依赖 `loadCaseAnnotations` 的 `len(ids)==0` 报错 + `len(annotations)!=len(uniqueIDs(ids))` 报错（L462-478）；若这两处校验被破坏，空切片索引 → panic。
- **`validateComparableAnnotations`（L308-341）**：访问 `annotation.Annotator.Username`、`annotation.Schema` 依赖 ByIDs 的 Preload；`index==0` 时取 schema，后续比较 `annotation.SchemaID != schema.ID`。
- **`summarizeEvidence`（L378-409）**：evidence 为空时 `selected` 默认 DisagreementLabel、`pairs[selected]` 为 nil map（range 安全）；但 Compute 在空 evidence 时已先返回 Unprocessable，故正常路径不会到这。
- **`matching.dominantDisagreement`（span.go L180-215）**：evidence 为空时 `pairs[""]` nil map，range 安全。
- **`config.seed`（L100-237）**：`users["manager"]` 等 map 取值——前面查询失败会 return err，正常不会零值。
- **`auth.writeAuthError`（auth.go L30-35）**：`err.(*service.AppError)` 类型断言失败返回 nil 的 appError 分支安全（重新构造），但包装错误会丢失原始码。
- **repository 返回零值结构体**：`Get` 失败时返回零值 model（非指针），调用方如继续访问 `.Dataset.DatasetCode` 得空串（安全但不干净）。

---

## 7. context 使用

- **仅 2 处使用 context**：`handler/system.go Ready` L56 的 `sqlDB.PingContext(context.Request.Context())`；`cmd/server/main.go` L48 的 `context.WithTimeout(context.Background(),10s)` 优雅关闭。
- **service/repository 完全不接收/不传播 context**：所有 GORM 调用均为 `repository.db.xxx`，**未用 `WithContext`**——HTTP 请求取消不会中断 DB 操作；也没有 `context.Context` 参数贯穿 handler→service→repository。
- **Background 滥用点**：无显式 Background 滥用（除 main 启动/关闭外），但"整个 service 层丢弃请求上下文"是结构性缺口，超时/取消语义全部失效。

---

## 8. 公开入口路由表（方法 + 路径 + 权限 + 限流 → service 函数）

全局中间件：RequestID → ErrorHandler → Recovery → CORS → **RateLimit(300/min, "global")** → Audit；`/api/v1/*` 一律 Auth(JWT)。

| 方法+路径 | 权限(RBAC) | 限流 | handler → service |
|---|---|---|---|
| GET /healthz | 公开 | global | Health |
| GET /readyz | 公开 | global | Ready（PingContext） |
| POST /api/v1/auth/login | 公开 | 30/min login | Login |
| GET /api/v1/datasets | 任意登录 | global | CorpusDataset.List |
| GET /api/v1/datasets/:id | 任意登录 | global | Get |
| POST /api/v1/datasets | data_manager/admin | global | Create |
| PUT /api/v1/datasets/:id | data_manager/admin | global | Update |
| POST /api/v1/datasets/:id/transition | data_manager/admin | global | Transition |
| GET /api/v1/schemas | 任意登录 | global | AnnotationSchema.List |
| GET /api/v1/schemas/:id | 任意登录 | global | Get |
| POST /api/v1/schemas | data_manager/admin | global | Create |
| PUT /api/v1/schemas/:id | data_manager/admin | global | Update |
| POST /api/v1/schemas/:id/copy | data_manager/admin | global | Copy |
| POST /api/v1/schemas/:id/transition | data_manager/admin | global | Transition |
| GET /api/v1/annotations | 任意登录 | global | AnnotationSet.List |
| GET /api/v1/annotations/:id | 任意登录 | global | Get |
| POST /api/v1/annotations | annotator/admin | global | Create |
| PUT /api/v1/annotations/:id | annotator/admin | global | Update |
| POST /api/v1/annotations/:id/transition | annotator/data_manager/adjudicator/admin | 60/min annotation_submit | Transition |
| GET /api/v1/adjudications | 任意登录 | global | AdjudicationCase.List |
| GET /api/v1/adjudications/:id | 任意登录 | global | Get |
| POST /api/v1/adjudications | data_manager/admin | 30/min agreement_compute | Compute（Idempotency-Key 头） |
| POST /api/v1/adjudications/:id/assign | adjudicator/admin | global | Assign |
| POST /api/v1/adjudications/:id/decide | adjudicator/admin | 30/min adjudication_submit | Decide（Idempotency-Key 头） |
| POST /api/v1/adjudications/:id/review | adjudicator/admin | global | Review |
| POST /api/v1/adjudications/:id/accept | adjudicator/admin | global | Accept |
| POST /api/v1/adjudications/:id/reopen | adjudicator/admin | global | Reopen |
| GET /api/v1/audit | auditor/adjudicator/admin | global | System.ListAudit |
| 其他 | - | - | NoRoute → 404 |

---

## 9. 现有 _test.go 覆盖

| 文件 | 覆盖行为 | 工具 |
|---|---|---|
| algorithm/agreement_test.go | CohenKappa 已知夹具(0.6875)、Krippendorff 缺失值、指标边界(无共享评分/单标注者/选指标) | 纯函数断言，无 DB |
| constants/annotation_state_test.go | 标注集/案件/数据集/规范四张迁移表合法与非法边 | 纯函数断言 |
| matching/span_test.go | CompareAnnotations 四类分歧计数、完全一致时 evidence 空+confusion=2、ClusterKey 稳定性 | 纯函数断言 |
| middleware/rbac_test.go | RBAC 允许/拒绝/缺失 actor（gin.New+httptest，无 DB） | httptest |
| service/system_test.go | encodeSummary 递归脱敏（labels/raw_text/examples/token/password_hash 全部打码）、adjudicationResponse 保留 agreement 元数据 | 纯函数 |
| service/transaction_test.go | 审计写入失败时 dataset 创建回滚、case 迁移回滚、Accept 仅限 reviewer、标注迁移授权表 | **sqlite 内存库（mode=memory&cache=shared）+ AutoMigrate + GORM 回调注入审计失败** |

**未覆盖**：handler 层（除 RBAC 外无 httptest 路由测试）、repository 层独立测试、Compute/Decide 幂等与并发、RateLimit/CORS/Auth 中间件、seed、List 分页/过滤、confusion/evidence 持久化往返。测试无 testify，仅标准库 + sqlite。

---

## 10. 候选埋点区域（10 个，跨模块分散）

1. **concurrency** — `repository/adjudication_case.go` `Decide` L94-110。条件更新 `WHERE id AND case_state='assigned' AND adjudicator_id=?` 是越权+并发防线；埋点：删掉 `adjudicator_id` 或 `case_state` 条件 / 把 `RowsAffected!=1` 改为返回 nil / 把 `decision_idempotency_key` 写成固定值。影响：POST /adjudications/:id/decide（越权裁决、状态乱跳、幂等失效）。
2. **state_pollution** — `repository/annotation_set.go` `Transition` L82-94 与 `UpdateDraft` L69-80。埋点：`submitted_at` 改在任何目标态都写入（或 returned→draft 时保留错误时间）、把 `updated_at = ?` 换成 `version`/删掉 `annotator_id` 条件。影响：POST /annotations/:id/transition、PUT /annotations/:id（时间戳污染、他人 draft 可改）。
3. **cross_layer_state** — `service/annotation_schema.go` `Copy` L121-146 对比 `Create` L35-37：Create 拒绝 archived 数据集，Copy 无此检查；埋点：在 Copy 中"补"一个错误的跳过逻辑或去掉 Create 的 archived 校验。影响：POST /schemas/:id/copy、POST /schemas（归档数据集可产新规范）。
4. **slice 污染** — `service/adjudication_case.go` `adjudicationInputHash` L411-419：原地 `sort.Slice(annotations)` 修改入参切片且哈希拼了 LabelsJSON 原文；埋点：去掉排序（哈希依赖传入顺序）/ 漏拼 dataset_id 或 version。影响：POST /adjudications 幂等与去重（同输入不同 hash → 重复案件）。
5. **runtime_crash（整数溢出）** — `handler/system.go` `Pagination` L119-132 + 5 个 repository 的 `Offset((page-1)*pageSize)`（adjudication_case.go L57 等）。`page` 无上限，`strconv.Atoi` 可吃进 2^63-1 → `(page-1)*pageSize` 溢出为负 → Offset 负数/SQL 错误。埋点：去掉 page 上限或把乘法改成减法。影响：所有列表 API（datasets/schemas/annotations/adjudications/audit）。
6. **error_propagation** — `service/system.go` `MapRepositoryError` L64-69：非 NotFound 一律 500；埋点：把唯一冲突/状态冲突也映射成 NotFound(404)（或把 record-not-found 判定改成只看 `errors.Is` 导致包装错误落 500）。影响：所有 Get/Detail 与 create 冲突的错误码（客户端分支判断全乱）。
7. **concurrency / 幂等兜底** — `service/adjudication_case.go` `Compute` L95-127：事务内 locked→compared 迁移 + 唯一冲突兜底（L116-124）；埋点：删掉 `annotation.AnnotationState == AnnotationLocked` 判断（把 compared 再转 compared）/ 把兜底分支的 `existing.InputHash==inputHash` 条件改反。影响：POST /adjudications（标注集状态破坏、幂等键误报 conflict）。
8. **algorithm/math** — `algorithm/agreement.go` `KrippendorffAlpha` L72-133 或 `normalizedAgreement` L180-188：埋点：`totalRatings*(totalRatings-1)` 分母改成 `totalPairs`、去掉 clamp、`round6` 精度改动、CohenKappa 的 missing 计数。影响：POST /adjudications 的 score/observed/chance 输出（种子对比与下游聚类依赖）。
9. **nil 解引用** — `service/adjudication_case.go` `Decide` L211-215 `annotations[0].Schema`（依赖 `loadCaseAnnotations` L462-478 的完整性校验）：埋点：删掉 `len(ids)==0` 报错或让 `ByIDs` 在空 ids 时返回空切片+nil error → 索引越界 panic（被 Recovery 兜成 500）。影响：POST /adjudications/:id/decide。
10. **context_lifecycle** — 全 service/repository 层零 context 传递（§7）；埋点：给某个 repository（如 `system.go ListAudit` 或 `CreateAudit`）加 `WithContext` 但 handler 用 `context.Background()`（或反向：某条 DB 调用改用超时 context 而其余不用）。影响：全部 API 的超时/取消语义不一致，长查询不随请求取消。

---

## 附：结构速览
```
cmd/server/main.go         启动/优雅关闭
internal/config             Load/OpenDatabase/seed（含演示数据）
internal/constants          状态机迁移表 + 角色/类型校验
internal/dto                请求/响应/校验 tag
internal/model              GORM 模型（唯一索引即幂等约束）
internal/repository         5 个 repo：条件更新 + RowsAffected 冲突
internal/service            AppError/审计/登录 + 4 个业务 service（事务编排）
internal/handler            Gin 薄壳 + 公共工具（Bind/Pagination/WriteError）
internal/middleware         Auth/RBAC/RateLimit/CORS/Recovery/Audit/RequestID/ErrorHandler
internal/router             Gin 路由 + wire 依赖组装
internal/algorithm          一致性算法（CohenKappa/Krippendorff）
internal/matching           span/分类差异比对 + ClusterKey
```
