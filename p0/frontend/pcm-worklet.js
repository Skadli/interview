// AudioWorklet：把麦克风音频下采样到 16kHz / 单声道 / Int16 PCM，分帧（512样本≈32ms）回传主线程。
class PCMWorklet extends AudioWorkletProcessor {
  constructor() {
    super();
    this.targetRate = 16000;
    this.ratio = sampleRate / this.targetRate; // sampleRate 为全局输入采样率
    this.acc = [];        // 输入样本缓存
    this.pos = 0;         // 浮点读取游标（输入样本单位）
    this.frame = 512;     // 输出帧长
    this.out = new Float32Array(this.frame);
    this.outIdx = 0;
  }

  process(inputs) {
    const input = inputs[0];
    if (!input || input.length === 0 || !input[0]) return true;
    const ch = input[0];
    for (let i = 0; i < ch.length; i++) this.acc.push(ch[i]);

    // 线性插值下采样
    while (this.pos + 1 < this.acc.length) {
      const i0 = Math.floor(this.pos);
      const frac = this.pos - i0;
      const s = this.acc[i0] * (1 - frac) + this.acc[i0 + 1] * frac;
      this.out[this.outIdx++] = s;
      this.pos += this.ratio;
      if (this.outIdx >= this.frame) {
        const i16 = new Int16Array(this.frame);
        for (let k = 0; k < this.frame; k++) {
          let v = Math.max(-1, Math.min(1, this.out[k]));
          i16[k] = v < 0 ? v * 0x8000 : v * 0x7fff;
        }
        this.port.postMessage(i16.buffer, [i16.buffer]);
        this.outIdx = 0;
      }
    }
    // 丢弃已消费的输入前缀
    const consumed = Math.floor(this.pos);
    if (consumed > 0) {
      this.acc.splice(0, consumed);
      this.pos -= consumed;
    }
    return true;
  }
}
registerProcessor("pcm-worklet", PCMWorklet);
