package canvasworkflow

import (
	"fmt"
	"regexp"
	"sort"
)

var nodeMentionPattern = regexp.MustCompile(`@\[node:([^\]\s]+)\]`)

func NodeMentionToken(id string) string { return "@[node:" + id + "]" }

func PromptBinding(field string) bool { return field == "prompt" || field == "promptMention" }

func MediaField(kind string) string {
	switch kind {
	case "image":
		return "referenceImages"
	case "video":
		return "referenceVideos"
	case "audio":
		return "referenceAudios"
	}
	return ""
}

// Names are presentation only. Bind stable node identities to the provider's
// actual reference slots, without exposing that numbering in the editor.
func NodePromptBindings(prompt string, bindings []Binding, nodes map[string]Node, edges []Edge, target string) ([]Binding, error) {
	var mentions []Binding
	for _, match := range nodeMentionPattern.FindAllStringSubmatchIndex(prompt, -1) {
		id := prompt[match[2]:match[3]]
		node, exists := nodes[id]
		connected := false
		for _, edge := range edges {
			if edge.From == id && edge.To == target {
				connected = true
				break
			}
		}
		if !exists || !connected {
			return nil, BadRequest("提示词引用的工作流节点已断开或删除，请重新选择")
		}
		binding := Binding{NodeID: id, Field: "prompt", Index: match[0], Original: prompt[match[0]:match[1]]}
		if node.MediaType != "text" {
			index := -1
			for _, media := range bindings {
				if media.NodeID != id || media.Field != MediaField(node.MediaType) {
					continue
				}
				if index >= 0 && index != media.Index {
					return nil, BadRequest("节点 " + node.Title + " 对应多个参考位置，请保留一条引用连线")
				}
				index = media.Index
			}
			if index < 0 {
				return nil, BadRequest("节点 " + node.Title + " 未建立有效的素材引用")
			}
			binding.Field = "promptMention"
			binding.ReferenceLabel = fmt.Sprintf("@%s%d", map[string]string{"image": "图片", "video": "视频", "audio": "音频"}[node.MediaType], index+1)
		}
		mentions = append(mentions, binding)
	}
	return mentions, nil
}

// RenderPromptBindings 按原始位置一次替换，输入文本中的其他占位符不会触发二次替换。
func RenderPromptBindings(prompt string, bindings []Binding, steps map[string]*Step) (string, error) {
	ordered := append([]Binding(nil), bindings...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Index > ordered[j].Index })
	previous := len(prompt) + 1
	for _, binding := range ordered {
		if !PromptBinding(binding.Field) {
			continue
		}
		value := steps[binding.NodeID]
		if value == nil || value.Status != "succeeded" {
			return "", BadRequest("执行步骤的输入尚未就绪")
		}
		end := binding.Index + len(binding.Original)
		if binding.Index < 0 || end > len(prompt) || end > previous || prompt[binding.Index:end] != binding.Original {
			return "", BadRequest("提示词变量位置失效，请重新保存工作流")
		}
		replacement := value.Text
		if binding.Field == "promptMention" {
			replacement = binding.ReferenceLabel
		}
		prompt = prompt[:binding.Index] + replacement + prompt[end:]
		previous = binding.Index
	}
	return prompt, nil
}
