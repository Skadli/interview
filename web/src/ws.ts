// WebSocket 客户端：JSON 控制 + 二进制 PCM 上行。

import type { ClientMsg, ServerMsg } from "./types";

export type WsState = "connecting" | "open" | "closed" | "error";

export class WsClient {
  private ws: WebSocket | null = null;
  private url: string;
  onMessage: (m: ServerMsg) => void = () => {};
  onState: (s: WsState) => void = () => {};

  constructor(url: string) {
    this.url = url;
  }

  get connected(): boolean {
    return this.ws !== null && this.ws.readyState === WebSocket.OPEN;
  }

  connect(): void {
    this.close();
    this.onState("connecting");
    const ws = new WebSocket(this.url);
    ws.binaryType = "arraybuffer";
    this.ws = ws;
    ws.onopen = () => this.onState("open");
    ws.onclose = () => this.onState("closed");
    ws.onerror = () => this.onState("error");
    ws.onmessage = (e) => {
      try {
        this.onMessage(JSON.parse(e.data) as ServerMsg);
      } catch {
        /* 忽略非 JSON */
      }
    };
  }

  send(m: ClientMsg): void {
    if (this.connected) this.ws!.send(JSON.stringify(m));
  }

  sendPcm(buf: ArrayBuffer): void {
    if (this.connected) this.ws!.send(buf);
  }

  close(): void {
    if (this.ws) {
      this.ws.onopen = this.ws.onclose = this.ws.onerror = this.ws.onmessage = null;
      try {
        this.ws.close();
      } catch {
        /* ignore */
      }
      this.ws = null;
    }
  }
}
