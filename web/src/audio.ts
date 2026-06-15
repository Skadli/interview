// AudioWorklet 16k Int16 PCM 采集。复用自 p0/frontend/app.js 的 ensureAudio 逻辑。
// 麦克风开启 echoCancellation/noiseSuppression/autoGainControl，降采样到 16k 单声道，
// 每帧（512 样本）通过回调上行二进制。

export class AudioCapture {
  private ctx: AudioContext | null = null;
  private stream: MediaStream | null = null;
  private node: AudioWorkletNode | null = null;
  private onFrame: (buf: ArrayBuffer) => void;

  constructor(onFrame: (buf: ArrayBuffer) => void) {
    this.onFrame = onFrame;
  }

  get ready(): boolean {
    return this.ctx !== null;
  }

  async ensure(): Promise<void> {
    if (this.ctx) return;
    this.stream = await navigator.mediaDevices.getUserMedia({
      audio: {
        echoCancellation: true,
        noiseSuppression: true,
        autoGainControl: true,
        channelCount: 1,
      },
    });
    const Ctor = window.AudioContext || (window as any).webkitAudioContext;
    this.ctx = new Ctor();
    if (this.ctx.state === "suspended") await this.ctx.resume();
    // pcm-worklet.js 位于 dist 根（public/），开发与生产均为 /pcm-worklet.js
    await this.ctx.audioWorklet.addModule("/pcm-worklet.js");
    const src = this.ctx.createMediaStreamSource(this.stream);
    this.node = new AudioWorkletNode(this.ctx, "pcm-worklet");
    this.node.port.onmessage = (e: MessageEvent) => this.onFrame(e.data as ArrayBuffer);
    src.connect(this.node); // 不连 destination，避免回放啸叫
  }

  async resume(): Promise<void> {
    if (this.ctx && this.ctx.state === "suspended") await this.ctx.resume();
  }

  stop(): void {
    this.node?.port.close();
    this.node?.disconnect();
    this.stream?.getTracks().forEach((t) => t.stop());
    this.ctx?.close();
    this.node = null;
    this.stream = null;
    this.ctx = null;
  }
}
