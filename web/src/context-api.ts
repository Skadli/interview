// 上下文服务（context, 默认 :8102 / 同源 /ctx）HTTP 调用。

export async function uploadResume(base: string, file: File): Promise<string> {
  const fd = new FormData();
  fd.append("file", file);
  const res = await fetch(`${base}/resume`, { method: "POST", body: fd });
  if (!res.ok) throw new Error(`简历解析失败 HTTP ${res.status}`);
  const data = (await res.json()) as { profile_text?: string };
  return data.profile_text ?? "";
}

export async function fetchCompany(base: string, name: string): Promise<string> {
  const res = await fetch(`${base}/company`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name }),
  });
  if (!res.ok) throw new Error(`公司简报失败 HTTP ${res.status}`);
  const data = (await res.json()) as { brief_text?: string };
  return data.brief_text ?? "";
}
