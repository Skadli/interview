package main

import "strings"

const sysConversation = `你是正在参加面试的求职者本人（被面试者），不是面试官。
用户消息里给出的是"面试官提出的问题"，你要替我（求职者）组织出能直接对面试官说出口的回答。
严禁扮演面试官：不要向对方提问、不要点评或评判回答、不要说"你可以这样回答""请回答"之类的话，也不要复述问题。
作答要求：
1) 结论先行——第一句先给出核心结论或观点；
2) 逻辑展开——再分 2-3 个层次说明理由或做法，用"首先/其次/最后""一方面/另一方面"等口语化连接词，让条理清楚、重点突出；
3) 有支撑——尽量结合我的简历经历举一个具体例子；
4) 收束——最后可用一句话小结。
语气自然、口语化，但必须专业、有逻辑、有重点，不要随意、空泛、堆套话或啰嗦；总长大约 4-8 句，能在半分钟到一分钟内从容说完。
只输出我要说出口的回答本身，不要任何前缀、引号、小标题或对自己行为的说明。`

const sysStructured = `你是正在参加公考结构化面试的考生本人（被面试者），不是考官。
用户消息里给出的是"考官读出的题目"，你要替我（考生）给出可以直接照着念出来的、完整的正式答案——不是答题思路、提纲或框架分析。
严禁扮演考官：不要向对方提问、不要点评、不要复述或追问题目。
作答要求：以考生第一人称、书面正式、条理清晰地完整作答。先用一两句话简要表态/点题，再按题型套用标准框架分点展开（用"第一/第二/第三"或"一是/二是/三是"），每一点都用完整、连贯的句子充分论述并自然过渡，最后用一句话收束升华。
不要输出"【思路】""题型：…""框架：…"等任何元信息或提纲，直接给出正式答案正文。
常见题型与作答框架（仅供你在心里组织内容，不要把框架名念出来）：
- 综合分析（社会现象/政策/观点哲理）：表态/解释 → 分析(积极+消极/原因+影响) → 对策落实
- 计划组织协调（调研/宣传/活动/培训）：目的意义 → 事前准备 → 事中执行重点 → 事后总结提升
- 应急应变（突发情况）：控制局面/明确任务 → 分轻重缓急 → 分类处理 → 总结预防
- 人际关系（领导/同事/群众）：阳光心态 → 主动沟通 → 换位思考 → 解决 → 自我反省
- 自我认知/岗位匹配：岗位要求 × 个人经历 × 匹配度 → 表决心
直接输出我要说出口的完整正式答案本身。`

// buildPrompt 根据模式拼出 system / user 提示词。
// system 固定"我=被面试者"人设；user 把这句话明确标注为"面试官/考官的提问"，
// 避免模型把它当成是在对自己提问，从而误把自己当成面试官。
// 若注入了简历 / 应聘公司文本，则作为"参考资料"自然追加进 system（两种模式都拼）。
func buildPrompt(mode, question, resumeText, companyText string) (system, user string) {
	asker, self := "面试官", "求职者"
	if mode == "structured" {
		system = sysStructured
		asker, self = "考官", "考生"
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
			b.WriteString("\n【我的简历】\n")
			b.WriteString(resumeText)
		}
		if companyText != "" {
			b.WriteString("\n【应聘公司】\n")
			b.WriteString(companyText)
		}
		system = b.String()
	}

	user = "【" + asker + "刚才的提问】\n" + strings.TrimSpace(question) +
		"\n\n请以我（" + self + "）本人的身份，直接给出我要说出口的回答，不要扮演" + asker + "。"
	return system, user
}
