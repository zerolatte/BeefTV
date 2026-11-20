package credits

import (
	"infinite-canvas/backend/internal/model"
	"testing"
)

func TestRefundRequiresCompleteUndispatchedEvidence(t *testing.T) {
	row := &model.Task{Status: model.TaskStatusFailed, FailureDiagnostics: &model.TaskFailureDiagnostics{ExecutionResult: "failed", Requests: []model.TaskRequestEvidence{{Dispatched: false, Outcome: "not_dispatched"}}}}
	if !confirmedNoOutput(row) {
		t.Fatal("明确未发出请求应允许退款")
	}
	row.FailureDiagnostics.OmittedRequests = 1
	if confirmedNoOutput(row) {
		t.Fatal("不完整请求证据不应退款")
	}
	row.FailureDiagnostics.OmittedRequests = 0
	row.FailureDiagnostics.Requests[0].Dispatched = true
	if confirmedNoOutput(row) {
		t.Fatal("已发出的不确定请求不应退款")
	}
	row.FailureDiagnostics = nil
	if confirmedNoOutput(row) {
		t.Fatal("没有执行证据不应退款")
	}
}

func TestCreditContinuousImagesRequiresMaximum(t *testing.T) {
	if _, err := unitsFor("image", map[string]any{"seedreamMode": "auto"}); err == nil {
		t.Fatal("连续组图未指定最大张数仍然报价")
	}
	units, err := unitsFor("image", map[string]any{"count": "1", "seedreamMode": "auto", "seedreamMaxImages": "4"})
	if err != nil || units != 4 {
		t.Fatalf("最大张数报价错误: %d %v", units, err)
	}
}
