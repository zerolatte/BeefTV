package canvasworkflow

import (
	"encoding/json"
	"fmt"
	"infinite-canvas/backend/internal/editing"
	"infinite-canvas/backend/internal/model"
	"math"
	"sort"
)

// timeline lowers trim and merge into the durable timeline render task. Its
// worker owns encoding, cancellation and resource recovery.
func (r *Runs) timeline(user, operation string, n Node, edges []Edge, steps map[string]*Step) (*model.Task, error) {
	project, options, err := TimelineProject(func(id string) (*model.Resource, error) { return r.resource(user, id, "video") }, n, edges, steps)
	if err != nil {
		return nil, err
	}
	return r.host.TimelineRender(user, operation, project, options)
}

// TimelineProject builds the timeline for a trim or merge step.
func TimelineProject(video func(id string) (*model.Resource, error), n Node, edges []Edge, steps map[string]*Step) (editing.Project, editing.Options, error) {
	incoming := make([]Edge, 0)
	for _, edge := range edges {
		if edge.To == n.ID {
			incoming = append(incoming, edge)
		}
	}
	sort.SliceStable(incoming, func(i, j int) bool { return incoming[i].Order < incoming[j].Order })
	visible := true
	project := editing.Project{Version: editing.ProjectVersion, Tracks: []editing.Track{{ID: "video", Kind: editing.KindVideo, Visible: &visible}}}
	options := editing.Options{}
	switch n.OperationID {
	case "video.trim":
		if len(incoming) != 1 {
			return project, options, BadRequest("视频裁剪需要一个视频输入")
		}
	case "video.merge":
		if len(incoming) < 2 {
			return project, options, BadRequest("视频合并需要至少两个视频输入")
		}
	default:
		return project, options, BadRequest("不支持的本地视频操作")
	}
	for i, edge := range incoming {
		step := steps[edge.From]
		if step == nil || step.Status != "succeeded" || step.ResourceID == "" {
			return project, options, BadRequest("视频输入尚未完成或没有已保存的素材")
		}
		resource, err := video(step.ResourceID)
		if err != nil {
			return project, options, err
		}
		start, duration := int64(0), resource.DurationMs
		if n.OperationID == "video.trim" {
			var end int64
			if value, ok := n.Config["startSeconds"]; ok {
				start, err = Seconds(value)
			}
			if err == nil {
				end, err = Seconds(n.Config["endSeconds"])
			}
			if err != nil || end <= start {
				return project, options, BadRequest("裁剪起止时间无效，结束时间必须大于开始时间")
			}
			if resource.DurationMs > 0 && end > resource.DurationMs {
				return project, options, BadRequest("裁剪结束时间不能超过视频时长")
			}
			duration = end - start
		}
		if duration <= 0 || duration > editing.MaxDurationMs-project.DurationMs {
			return project, options, BadRequest("视频时长缺失或超出支持范围，请先更新素材信息")
		}
		if i == 0 && resource.Width > 0 && resource.Height > 0 {
			// H.264 requires even dimensions; retain the first video's framing.
			options.Width = (resource.Width + 1) / 2 * 2
			options.Height = (resource.Height + 1) / 2 * 2
		}
		id := fmt.Sprintf("video-%d", i)
		project.Clips = append(project.Clips, editing.Clip{
			ID: id, NodeID: id, TrackID: "video", Kind: editing.KindVideo,
			StartMs: project.DurationMs, DurationMs: duration, SourceStartMs: start,
			SourceDurationMs: resource.DurationMs, Volume: 1,
			DirectMedia: &editing.DirectMedia{ID: id, Kind: editing.KindVideo, StorageKey: "resource:" + resource.ID},
		})
		project.DurationMs += duration
	}
	return project, options, nil
}

// Seconds converts a configured time in seconds to milliseconds.
func Seconds(value any) (int64, error) {
	var seconds float64
	switch number := value.(type) {
	case float64:
		seconds = number
	case int:
		seconds = float64(number)
	case int64:
		seconds = float64(number)
	case json.Number:
		var err error
		seconds, err = number.Float64()
		if err != nil {
			return 0, err
		}
	default:
		return 0, fmt.Errorf("时间必须是数字")
	}
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > float64(editing.MaxDurationMs)/1000 {
		return 0, fmt.Errorf("时间超出支持范围")
	}
	return int64(math.Round(seconds * 1000)), nil
}
