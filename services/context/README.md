# context service（简历解析 + 公司简报）

FastAPI 服务，端口 **8102**。为面试系统提供两类上下文：候选人画像（简历）与公司简报。

## HTTP 契约

### `POST /resume`
- 请求：`multipart/form-data`，字段 `file` = `pdf` / `png` / `jpg`
- 响应：`{"profile_text": "..."}`

解析逻辑：
1. **PDF**：用 PyMuPDF(fitz) 抽取文字。
   - 抽到足够文字（>= 40 字符）→ 视为文字版 PDF，用该文字。
   - 抽不到文字（扫描件 / 无文字层 PDF）→ 渲染首页为 PNG，走豆包 **vision**（doubao-seed-1.6）识别。
2. **图片（png/jpg）**：转 base64 data url，走豆包 **vision** 识别。
3. 拿到原始/识别文本后：
   - 配置了 `ARK_API_KEY` → 调豆包把文本结构化为简洁画像（教育/工作经历/项目/技能/亮点）。
   - 未配置 key（degrade）→ 直接返回抽取到的原始文本。

### `POST /company`
- 请求：`application/json`，`{"name": "公司名"}`
- 响应：`{"brief_text": "..."}`
  - 配置了 `ARK_API_KEY` → 调豆包生成简报（主营业务/规模/文化价值观/近期动态/面试可能关注点）。
  - 未配置 key（degrade）→ 返回公司名 + 一句占位说明。

### `GET /health`
- 响应：`{"status": "ok"}`

## ARK_API_KEY 配置

通过环境变量提供（不写入代码/配置文件）：

```powershell
$env:ARK_API_KEY = "your-ark-key"
```

豆包/方舟调用：`POST https://ark.cn-beijing.volces.com/api/v3/chat/completions`，
头 `Authorization: Bearer ARK_API_KEY`，模型 `doubao-seed-1.6`，
`thinking:{"type":"disabled"}`，非流式。vision 用 content 数组传入
`{"type":"image_url","image_url":{"url":"data:image/png;base64,..."}}`。

## degrade 行为（无 key）

| 输入 | 行为 |
|------|------|
| 文字版 PDF | 返回 PyMuPDF 抽取的原始文字 |
| 扫描件 PDF | 返回占位说明（vision 需要 key） |
| 图片简历 | 返回占位说明（vision 需要 key） |
| `/company` | 返回公司名 + 占位说明 |

Ark 调用失败（超时/异常）时也会降级：`/resume` 文字版回落到原始文字，
`/company` 回落到公司名 + 错误说明。

## 运行

```powershell
.\run.ps1
```

`run.ps1` 会建 venv、`pip install -r requirements.txt`、用 uvicorn 在 8102 启动。

依赖（均有 Python 3.14 轮子）：fastapi, uvicorn[standard], pymupdf, httpx, pillow, python-multipart。
