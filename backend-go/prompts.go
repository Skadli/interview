package main

import "strings"

const sysConversation = `你是面试实时助手。请用第一人称、口语化、自然的中文，给出可直接说出口的参考回答。
要求：先给结论，再补 1-2 句理由；总长 2-4 句；不要堆术语和套话；能结合候选人简历经历更好。
只输出参考回答本身，不要解释你在做什么。`

const sysStructured = `你是公考结构化面试答题助手。请按题型套用标准框架作答，输出"分点骨架(一是/二是/三是)+过渡语"，书面、正式、条理清晰。
常见题型与框架：
- 综合分析（社会现象/政策/观点哲理）：表态/解释 → 分析(积极+消极/原因+影响) → 对策落实
- 计划组织协调（调研/宣传/活动/培训）：目的意义 → 事前准备 → 事中执行重点 → 事后总结提升
- 应急应变（突发情况）：控制局面/明确任务 → 分轻重缓急 → 分类处理 → 总结预防
- 人际关系（领导/同事/群众）：阳光心态 → 主动沟通 → 换位思考 → 解决 → 自我反省
- 自我认知/岗位匹配：岗位要求 × 个人经历 × 匹配度 → 表决心
先给一行"【思路】"点明题型与框架，再展开分点作答。`

// buildPrompt 根据模式拼出 system / user 提示词。
// 若注入了简历 / 应聘公司文本，则作为"参考资料"自然追加进 system（两种模式都拼）。
func buildPrompt(mode, question, resumeText, companyText string) (system, user string) {
	if mode == "structured" {
		system = sysStructured
	} else {
		system = sysConversation
	}

	resumeText = strings.TrimSpace(resumeText)
	companyText = strings.TrimSpace(companyText)
	if resumeText != "" || companyText != "" {
		var b strings.Builder
		b.WriteString(system)
		b.WriteString("\n\n（以下为参考资料，作答时自然结合，不要照搬）")
		if resumeText != "" {
			b.WriteString("\n【候选人简历】\n")
			b.WriteString(resumeText)
		}
		if companyText != "" {
			b.WriteString("\n【应聘公司】\n")
			b.WriteString(companyText)
		}
		system = b.String()
	}

	return system, question
}
