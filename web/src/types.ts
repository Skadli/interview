// WebSocket 协议类型（前端 web 与 Go /ws）

export type Mode = "conversation" | "structured";
export type Speaker = "user" | "interviewer";

export interface Timing {
  endpoint_ms: number;
  speaker_ms: number;
  asr_ms: number;
  llm_ttft_ms: number;
  to_first_word_ms: number;
  perceived_first_word_ms: number;
  llm_total_ms: number;
}

// 服务端 -> 客户端
export type ServerMsg =
  | { type: "status"; state: string }
  | { type: "partial"; text: string }
  | { type: "transcript"; text: string; speaker: Speaker; similarity?: number }
  | { type: "question"; text: string }
  | { type: "answer_delta"; text: string }
  | { type: "answer_done"; timing: Timing }
  | { type: "enrolled"; ok: boolean; reason?: string };

// 客户端 -> 服务端
export type ClientMsg =
  | { type: "set_mode"; mode: Mode }
  | { type: "enroll_start" }
  | { type: "enroll_stop" }
  | { type: "enroll_cancel" }
  | { type: "set_context"; resume_text: string; company_text: string }
  | { type: "regenerate" };

export interface QAItem {
  question: string;
  answer: string;
}
