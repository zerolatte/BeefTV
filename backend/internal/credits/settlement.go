package credits

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"log"
	"strings"
	"time"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/task"
)

func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if err := s.Reconcile(); err != nil {
			log.Print("积分对账暂未完成，将自动重试")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) Reconcile() error {
	rows, err := s.repo.PendingCreditReservations(100)
	if err != nil {
		return err
	}
	for _, row := range rows {
		t, err := s.repo.TaskForUser(row.UserID, row.TaskID)
		if err != nil {
			return err
		}
		if t.Status == model.TaskStatusQueued || t.Status == model.TaskStatusRunning {
			continue
		}
		if t.LeaseOwner != "" && t.LeaseExpiresAt != nil && t.LeaseExpiresAt.After(time.Now()) {
			continue
		}
		units, err := s.validOutputs(t)
		if err != nil {
			return err
		}
		if units > row.Units {
			units = row.Units
		}
		confirmedEmpty := confirmedNoOutput(t)
		if (t.Status == model.TaskStatusSucceeded && units > 0) || confirmedEmpty || units == row.Units {
			if err := s.repo.SettleCredits(row.ID, units, "", "按已持久化有效产出结算"); err != nil {
				return err
			}
		} else if err := s.repo.MarkCreditReview(row.ID); err != nil {
			return err
		}
	}
	return nil
}

// 仅使用持久化执行证据退款，网络超时和遗漏的请求证据必须保留冻结。
func confirmedNoOutput(t *model.Task) bool {
	if t.Status == model.TaskStatusCancelled && t.StartedAt == nil {
		return true
	}
	if t.RecoveryOutcome != nil && *t.RecoveryOutcome == task.RecoveryNoResult {
		return true
	}
	d := t.FailureDiagnostics
	if t.Status != model.TaskStatusFailed || t.ProviderRequestID != "" || t.ImageJobID != "" || d == nil || d.ExecutionResult != "failed" || d.OmittedRequests != 0 || len(d.Requests) == 0 {
		return false
	}
	for _, request := range d.Requests {
		if request.Dispatched || request.Outcome != "not_dispatched" {
			return false
		}
	}
	return true
}

func (s *Service) validOutputs(t *model.Task) (int64, error) {
	if t.Type == "canvas_text" {
		if strings.TrimSpace(t.TextDraft) != "" {
			return 1, nil
		}
		var result struct {
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(t.ResultJSON), &result) == nil && strings.TrimSpace(result.Text) != "" {
			return 1, nil
		}
		return 0, nil
	}
	seen := map[string]bool{}
	for _, output := range task.CanonicalOutputs(t.ResultJSON, t.Type) {
		if output.ResourceID == "" || seen[output.ResourceID] {
			continue
		}
		resource, err := s.repo.ResourceForUser(t.UserID, output.ResourceID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return 0, err
		}
		if resource.Status == model.ResourceStatusReady {
			seen[resource.ID] = true
		}
	}
	return int64(len(seen)), nil
}

func (s *Service) Resolve(actor, id, reason string, units int64) error {
	if _, err := s.identity.RequireAdmin(actor); err != nil {
		return err
	}
	if strings.TrimSpace(reason) == "" || len(reason) > 500 {
		return kernel.BadAuthRequest("请填写核对依据")
	}
	row, err := s.repo.CreditReservation(id)
	if err != nil {
		return err
	}
	if row.State != "review" {
		return kernel.BadAuthRequest("只能核对待确认的冻结记录")
	}
	t, err := s.repo.TaskForUser(row.UserID, row.TaskID)
	if err != nil {
		return err
	}
	if t.Status == model.TaskStatusRunning || t.Status == model.TaskStatusQueued {
		return kernel.BadAuthRequest("任务尚未终止")
	}
	return s.repo.SettleCredits(id, units, actor, reason)
}
