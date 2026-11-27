package repository

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
)

func TestPlatformChannelDeleteAndAdmissionAreAtomic(t *testing.T) {
	for i := 0; i < 8; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			repo := creditRepository(t)
			channel, err := repo.SystemChannel("channel")
			if err != nil {
				t.Fatal(err)
			}
			catalogModel, err := repo.ChannelModel("model")
			if err != nil {
				t.Fatal(err)
			}
			quote := model.CreditQuote{ID: "race-quote", UserID: "user", ModelID: "model", PriceID: "price", PriceVersion: 1,
				RequestHash: "hash", UnitCredits: 4, Units: 1, TotalCredits: 4, ExpiresAt: time.Now().Add(time.Minute), ModelUpdatedAt: catalogModel.UpdatedAt, ChannelUpdatedAt: channel.UpdatedAt}
			if err := repo.CreateCreditQuote(&quote); err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			deleted, admitted := make(chan error, 1), make(chan error, 1)
			go func() {
				<-start
				deleted <- repo.DeletePlatformChannel("channel", model.PlatformAudit{ID: "race-audit", ActorID: "admin", SubjectID: "channel", Action: "channel.delete"})
			}()
			go func() {
				<-start
				row := model.Task{ID: "race-task", UserID: "user", Type: "canvas_text", Status: model.TaskStatusQueued, InputJSON: "{}"}
				admitted <- repo.AdmitCreditTask(&row, 100, quote.ID, "hash")
			}()
			close(start)
			deleteErr, admitErr := <-deleted, <-admitted
			account, err := repo.CreditAccount("user")
			if err != nil {
				t.Fatal(err)
			}
			if admitErr == nil {
				if !errors.Is(deleteErr, ErrPlatformChannelInUse) || account.Available != 6 || account.Frozen != 4 {
					t.Fatalf("任务已受理但渠道被删或账目不一致: delete=%v account=%+v", deleteErr, account)
				}
				if _, err := repo.SystemChannel("channel"); err != nil {
					t.Fatal("已受理任务的渠道丢失")
				}
			} else {
				if deleteErr != nil || account.Available != 10 || account.Frozen != 0 {
					t.Fatalf("删除优先时没有完整拒绝任务: delete=%v admit=%v account=%+v", deleteErr, admitErr, account)
				}
				if _, err := repo.SystemChannel("channel"); !errors.Is(err, gorm.ErrRecordNotFound) {
					t.Fatal("删除后渠道仍可使用")
				}
			}
		})
	}
}

func TestPlatformChannelDeleteChecksRetiredModelReservations(t *testing.T) {
	repo := creditRepository(t)
	if err := repo.db.Where("id = ?", "model").Delete(&model.ChannelModel{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.db.Create(&model.CreditReservation{ID: "retired-reservation", TaskID: "historical-task", QuoteID: "historical-quote", ModelID: "model", State: "review"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.DeletePlatformChannel("channel", model.PlatformAudit{ID: "audit"}); !errors.Is(err, ErrPlatformChannelInUse) {
		t.Fatalf("已退场模型的待核对积分未保护渠道: %v", err)
	}
}
