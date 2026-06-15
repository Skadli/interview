# -*- coding: utf-8 -*-
"""
Context service: resume parsing + company brief.

Endpoints (see README.md / project contract):
  POST /resume   multipart/form-data file=<pdf|png|jpg>  -> {"profile_text": "..."}
  POST /company  application/json {"name": "..."}         -> {"brief_text": "..."}
  GET  /health                                            -> {"status": "ok"}

Behavior:
  - PDF: extract text with PyMuPDF. If enough text -> use it. If it's a scanned /
    text-less PDF, OR the upload is an image -> use Doubao vision (doubao-seed-1.6)
    by converting the image to a base64 data URL.
  - With ARK_API_KEY set -> Doubao structures raw text into a concise profile.
  - Without ARK_API_KEY (degrade) -> return the raw extracted text as profile_text.

  - /company with ARK_API_KEY -> Doubao generates a company brief.
  - /company without key (degrade) -> company name + a placeholder note.

NOTE: no emoji anywhere (console is GBK on Windows).
"""

import base64
import io
import os

import fitz  # PyMuPDF
import httpx
from fastapi import FastAPI, File, UploadFile
from fastapi.responses import JSONResponse
from pydantic import BaseModel

# --- config ---------------------------------------------------------------

ARK_API_KEY = os.environ.get("ARK_API_KEY", "").strip()
ARK_URL = "https://ark.cn-beijing.volces.com/api/v3/chat/completions"
ARK_MODEL = "doubao-seed-1.6"

# Minimum chars of extracted PDF text to consider it a "text" PDF (not scanned).
MIN_PDF_TEXT_LEN = 40

# Ark request timeout (vision + structuring can be slow).
ARK_TIMEOUT = 60.0

app = FastAPI(title="context-service", version="1.0")


# --- models ---------------------------------------------------------------

class CompanyReq(BaseModel):
    name: str = ""


# --- Ark helpers ----------------------------------------------------------

def _ark_messages_text(prompt: str):
    return [{"role": "user", "content": prompt}]


def _ark_messages_vision(prompt: str, data_url: str):
    return [
        {
            "role": "user",
            "content": [
                {"type": "text", "text": prompt},
                {"type": "image_url", "image_url": {"url": data_url}},
            ],
        }
    ]


def _ark_chat(messages) -> str:
    """Non-stream Ark call. Returns assistant text. Raises on HTTP/parse error."""
    headers = {
        "Authorization": f"Bearer {ARK_API_KEY}",
        "Content-Type": "application/json",
    }
    payload = {
        "model": ARK_MODEL,
        "messages": messages,
        "stream": False,
        "thinking": {"type": "disabled"},
    }
    with httpx.Client(timeout=ARK_TIMEOUT) as client:
        resp = client.post(ARK_URL, headers=headers, json=payload)
        resp.raise_for_status()
        data = resp.json()
    return data["choices"][0]["message"]["content"]


# --- image helpers --------------------------------------------------------

_IMAGE_MIME = {
    "png": "image/png",
    "jpg": "image/jpeg",
    "jpeg": "image/jpeg",
}


def _guess_image_mime(filename: str, content_type: str) -> str:
    if content_type and content_type.startswith("image/"):
        return content_type
    ext = (filename or "").rsplit(".", 1)[-1].lower()
    return _IMAGE_MIME.get(ext, "image/png")


def _to_data_url(raw: bytes, mime: str) -> str:
    b64 = base64.b64encode(raw).decode("ascii")
    return f"data:{mime};base64,{b64}"


def _pdf_first_page_to_png(raw: bytes) -> bytes:
    """Render the first page of a (scanned) PDF to PNG bytes for vision OCR."""
    doc = fitz.open(stream=raw, filetype="pdf")
    try:
        page = doc.load_page(0)
        # 2x zoom for readability.
        pix = page.get_pixmap(matrix=fitz.Matrix(2, 2))
        return pix.tobytes("png")
    finally:
        doc.close()


def _pdf_extract_text(raw: bytes) -> str:
    doc = fitz.open(stream=raw, filetype="pdf")
    try:
        parts = []
        for page in doc:
            parts.append(page.get_text())
        return "\n".join(parts).strip()
    finally:
        doc.close()


# --- prompts --------------------------------------------------------------

PROFILE_PROMPT = (
    "你是简历分析助手。请把下面的简历原文整理成简洁的候选人画像，"
    "分为：教育背景、工作经历、项目经历、技能、亮点 五个部分，"
    "每部分用要点列出，去掉无关排版噪声。只输出画像本身。\n\n简历原文：\n"
)

VISION_PROMPT = (
    "这是一份简历的图片（可能是扫描件或截图）。请先读取其中全部文字，"
    "再整理成简洁的候选人画像，分为：教育背景、工作经历、项目经历、技能、亮点 五个部分，"
    "每部分用要点列出。只输出画像本身。"
)

VISION_EXTRACT_PROMPT = (
    "这是一份简历的图片（可能是扫描件或截图）。请逐字读取并输出其中的全部文字内容，"
    "保持原始信息，不要额外评论。"
)


def _company_prompt(name: str) -> str:
    return (
        f"请为公司「{name}」生成一份面试用简报，包含：主营业务、规模、"
        "文化价值观、近期动态、面试可能关注点 五个部分，每部分简明扼要。"
        "若信息不确定请标注。只输出简报本身。"
    )


# --- routes ---------------------------------------------------------------

@app.get("/health")
def health():
    return {"status": "ok"}


@app.post("/resume")
async def resume(file: UploadFile = File(...)):
    raw = await file.read()
    filename = file.filename or ""
    content_type = file.content_type or ""
    ext = filename.rsplit(".", 1)[-1].lower() if "." in filename else ""

    is_pdf = (ext == "pdf") or content_type == "application/pdf"

    profile_text = ""

    try:
        if is_pdf:
            text = ""
            try:
                text = _pdf_extract_text(raw)
            except Exception:
                text = ""

            if len(text) >= MIN_PDF_TEXT_LEN:
                # Text PDF.
                if ARK_API_KEY:
                    try:
                        profile_text = _ark_chat(
                            _ark_messages_text(PROFILE_PROMPT + text)
                        )
                    except Exception:
                        # Ark failed -> degrade to raw text.
                        profile_text = text
                else:
                    profile_text = text
            else:
                # Scanned / text-less PDF -> render to image, use vision.
                if ARK_API_KEY:
                    try:
                        png = _pdf_first_page_to_png(raw)
                        data_url = _to_data_url(png, "image/png")
                        profile_text = _ark_chat(
                            _ark_messages_vision(VISION_PROMPT, data_url)
                        )
                    except Exception as e:
                        profile_text = f"[无法解析的PDF，且vision调用失败] {e}"
                else:
                    profile_text = (
                        "[未配置ARK_API_KEY，且该PDF无可提取文字（疑似扫描件），"
                        "无法在degrade模式下解析]"
                    )
        else:
            # Image upload (png/jpg).
            mime = _guess_image_mime(filename, content_type)
            if ARK_API_KEY:
                try:
                    data_url = _to_data_url(raw, mime)
                    profile_text = _ark_chat(
                        _ark_messages_vision(VISION_PROMPT, data_url)
                    )
                except Exception as e:
                    profile_text = f"[图片简历vision调用失败] {e}"
            else:
                profile_text = (
                    "[未配置ARK_API_KEY，图片简历需要vision识别，"
                    "无法在degrade模式下解析]"
                )
    except Exception as e:
        return JSONResponse(
            status_code=500,
            content={"profile_text": "", "error": f"resume parse failed: {e}"},
        )

    return {"profile_text": profile_text.strip()}


@app.post("/company")
def company(req: CompanyReq):
    name = (req.name or "").strip()
    if not name:
        return JSONResponse(
            status_code=400,
            content={"brief_text": "", "error": "name is required"},
        )

    if ARK_API_KEY:
        try:
            brief = _ark_chat(_ark_messages_text(_company_prompt(name)))
            return {"brief_text": brief.strip()}
        except Exception as e:
            # Degrade on Ark failure.
            return {
                "brief_text": f"{name}（公司简报生成失败，已降级；error: {e}）"
            }

    # Degrade: no key.
    return {
        "brief_text": (
            f"{name}（未配置ARK_API_KEY，暂无法生成详细公司简报，"
            "请在服务端设置 ARK_API_KEY 后重试）"
        )
    }
