//go:build platform

package platformquota

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformauth"
)

type ReviewInput struct {
	ClientOperationID string        `json:"clientOperationId"`
	ExpectedState     string        `json:"expectedState"`
	Fact              ExecutionFact `json:"fact"`
	Reason            string        `json:"reason"`
}

// Review 只允许管理员根据已核实证据处理超时意图，并保留原始处理记录。
func (s *Service) Review(ctx context.Context, actorID, intentID string, input ReviewInput) (*model.PlatformExecutionReview, error) {
	if err := validateRequestKey(input.ClientOperationID); err != nil {
		return nil, err
	}
	input.Reason = strings.TrimSpace(input.Reason)
	state := ""
	switch input.Fact {
	case FactSucceeded:
		state = StateSettled
	case FactNotAccepted, FactFailedNoCost:
		state = StateReleased
	case FactFailedConsumed:
		state = StateCompensated
	default:
		return nil, kernel.BadAuthRequest("人工核对必须填写已确认的终局事实")
	}
	if input.Reason == "" || !utf8.ValidString(input.Reason) || utf8.RuneCountInString(input.Reason) > 500 {
		return nil, kernel.BadAuthRequest("请填写 1—500 字核对依据，不包含提示词或凭据")
	}
	var result *model.PlatformExecutionReview
	err := s.transaction(ctx, func(tx *gorm.DB) error {
		if err := requireAdmin(tx, actorID); err != nil {
			return err
		}
		var previous model.PlatformExecutionReview
		err := tx.Where("actor_account_id = ? AND request_key = ?", actorID, input.ClientOperationID).Take(&previous).Error
		if err == nil {
			if previous.IntentID != intentID || previous.PreviousState != input.ExpectedState || previous.Fact != string(input.Fact) || previous.Reason != input.Reason {
				return failure(409, "idempotency_conflict", "同一标识对应了不同核对操作")
			}
			result = &previous
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var intent model.PlatformExecutionIntent
		if err := tx.Where("id = ?", intentID).Take(&intent).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return kernel.NotFound("执行意图不存在")
			}
			return err
		}
		if intent.State != input.ExpectedState || terminal(intent.State) {
			return failure(409, "settlement_conflict", "执行状态已经变化，请刷新后核对")
		}
		if intent.SubmittedAt == nil || intent.SubmittedAt.After(s.now().Add(-24*time.Hour)) {
			return failure(409, "review_not_due", "执行尚未进入人工核对期限")
		}
		if err := s.finalize(tx, &intent, state, "人工核对："+input.Reason); err != nil {
			return err
		}
		result = &model.PlatformExecutionReview{ID: kernel.NewID(), IntentID: intentID, ActorAccountID: actorID, RequestKey: input.ClientOperationID, PreviousState: input.ExpectedState, Fact: string(input.Fact), Reason: input.Reason, CreatedAt: s.now()}
		if err := tx.Create(result).Error; err != nil {
			return err
		}
		difference, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Create(&platformauth.Audit{ID: kernel.NewID(), ActorAccountID: actorID, TargetAccountID: intent.AccountID, Action: "execution_reviewed", Difference: string(difference), CreatedAt: s.now()}).Error
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
