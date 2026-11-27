package bootstrap

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformadmin"
	"infinite-canvas/backend/internal/repository"
)

func TestPlatformChannelDeletePreservesHistoryAndInvalidatesCatalog(t *testing.T) {
	f := newCatalogFixture(t)
	user := f.user(t)
	channel := f.createChannel(t, "删除渠道验收", catalogModel("delete-text", "text", "chat-completion", catalogVariant(nil, catalogAmount(4))))
	other := f.createChannel(t, "保留渠道")
	item := f.models(t, channel.ID, "").Items[0]
	f.admin.call(t, "PUT", "/admin/prices", map[string]any{"modelId": item.ID, "variantId": item.Variants[0].ID, "unitCredits": 4, "published": true}, 200)
	f.admin.call(t, "POST", "/admin/accounts/"+user.userID+"/credits", map[string]any{"amount": 100, "operationId": "delete-history-credit", "reason": "删除行为测试"}, 200)
	if !catalogUserModels(t, user)[item.ModelKey] {
		t.Fatal("发布后用户目录缺少测试模型")
	}
	repo := repository.NewPlatform(f.runtime.db)
	request := map[string]any{"type": "canvas_text", "operation": "text", "prompt": "合成测试", "model": item.ModelKey,
		"input": map[string]any{"mode": "text", "prompt": "合成测试", "config": map[string]any{"channelId": channel.ID, "model": item.ModelKey},
			"metadata": map[string]any{"clientOperationId": "delete-history-operation"}}}
	quote := catalogData[model.CreditQuote](t, user.call(t, "POST", "/tasks/quote", request, 200))
	request["quoteId"] = quote.ID
	history := catalogData[model.Task](t, user.call(t, "POST", "/tasks", request, 200))
	if result := f.admin.call(t, "DELETE", "/admin/channels/"+channel.ID, nil, 409); result["reason"] != "channel_in_use" {
		t.Fatal("真实受理任务未阻止删除")
	}
	if err := f.runtime.db.Model(&model.Task{}).Where("id = ?", history.ID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": `{"text":"保留的历史结果"}`}).Error; err != nil {
		t.Fatal(err)
	}
	var reservation model.CreditReservation
	if err := f.runtime.db.First(&reservation, "task_id = ?", history.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.SettleCredits(reservation.ID, 1, "", "测试结果已交付"); err != nil {
		t.Fatal(err)
	}
	if err := f.runtime.db.First(&reservation, "id = ?", reservation.ID).Error; err != nil {
		t.Fatal(err)
	}
	request["input"].(map[string]any)["metadata"] = map[string]any{"clientOperationId": "delete-unused-operation"}
	quote = catalogData[model.CreditQuote](t, user.call(t, "POST", "/tasks/quote", request, 200))
	request["quoteId"] = quote.ID
	beforeLedger := user.call(t, "GET", "/credits/ledger", nil, 200)
	beforeHistory := user.call(t, "GET", "/tasks/"+history.ID, nil, 200)
	beforePrice, err := repo.CreditPrice(item.ID, item.Variants[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟已存密钥及自定义头，删除后只保留渠道历史身份。
	if err := f.runtime.db.Model(&model.ModelChannel{}).Where("id = ?", channel.ID).Updates(map[string]any{"secret_key": "synthetic-encrypted-value", "headers_json": `[{"name":"X-Test","value":"synthetic-header"}]`}).Error; err != nil {
		t.Fatal(err)
	}
	result := f.admin.call(t, "DELETE", "/admin/channels/"+channel.ID, nil, 200)
	if data(result)["deleted"] != true {
		t.Fatal("删除响应未确认完成")
	}
	page := catalogData[platformadmin.ChannelPage](t, f.admin.call(t, "GET", "/admin/channels", nil, 200))
	if page.Total != 1 || page.Channels[0].ID != other.ID || catalogUserModels(t, user)[item.ModelKey] {
		t.Fatal("删除后管理目录或用户目录没有同步，或误删了其他渠道")
	}
	var removed model.ModelChannel
	if err := f.runtime.db.Unscoped().First(&removed, "id = ?", channel.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !removed.DeletedAt.Valid || removed.Enabled || removed.APIKey != "" || removed.SecretKey != "" || removed.HeadersJSON != "[]" || removed.ModelsJSON != "[]" || removed.Name != channel.Name {
		t.Fatal("渠道软删除没有清除凭据与模型索引，或丢失历史名称")
	}
	var removedModel model.ChannelModel
	var removedVariant model.ChannelModelVariant
	if err := f.runtime.db.Unscoped().First(&removedModel, "id = ?", item.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.runtime.db.Unscoped().First(&removedVariant, "id = ?", item.Variants[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if !removedModel.DeletedAt.Valid || removedModel.Enabled || !removedVariant.DeletedAt.Valid || removedVariant.Enabled {
		t.Fatal("关联模型与规格未同步软删除和停用")
	}
	price, err := repo.CreditPrice(item.ID, item.Variants[0].ID)
	if err != nil || price.Published || price.Version != beforePrice.Version+1 || price.UnitCredits != beforePrice.UnitCredits {
		t.Fatal("删除没有撤销发布、失效旧价格版本或保留原价格")
	}
	status, _, err := catalogContextCall(user, context.Background(), "POST", "/tasks", request)
	if err != nil || status == 200 {
		t.Fatalf("已删除渠道仍可通过旧报价受理: status=%d err=%v", status, err)
	}
	if !reflect.DeepEqual(beforeHistory, user.call(t, "GET", "/tasks/"+history.ID, nil, 200)) || !reflect.DeepEqual(beforeLedger, user.call(t, "GET", "/credits/ledger", nil, 200)) {
		t.Fatal("删除影响历史任务或积分流水")
	}
	var after model.CreditReservation
	if err := f.runtime.db.First(&after, "id = ?", reservation.ID).Error; err != nil || !reflect.DeepEqual(reservation, after) {
		t.Fatal("删除影响已受理任务的积分快照")
	}
	var audits []model.PlatformAudit
	if err := f.runtime.db.Where("subject_id = ? AND action = ?", channel.ID, "channel.delete").Find(&audits).Error; err != nil || len(audits) != 1 || audits[0].ActorID != f.admin.userID {
		t.Fatal("删除审计缺失")
	}
	f.admin.call(t, "DELETE", "/admin/channels/"+channel.ID, nil, 404)
	f.admin.call(t, "GET", "/admin/channels/"+channel.ID+"/models", nil, 404)
	f.admin.call(t, "POST", "/admin/channels/probe", map[string]any{"channelId": channel.ID}, 404)
}

func TestPlatformChannelDeletePermissionAndBusyProtection(t *testing.T) {
	f := newCatalogFixture(t)
	user := f.user(t)
	channel := f.createChannel(t, "删除占用验收", catalogModel("busy-audio", "audio", "openai-audio"))
	item := f.models(t, channel.ID, "").Items[0]
	path := "/admin/channels/" + channel.ID
	user.call(t, "DELETE", path, nil, 403)
	service := platformadmin.New(f.auth, repository.NewPlatform(f.runtime.db), platformadmin.Options{})
	if err := service.DeleteChannel(user.userID, channel.ID); err == nil {
		t.Fatal("直接调用服务绕过了管理员校验")
	}
	csrf := f.admin.csrf
	f.admin.csrf = "invalid"
	f.admin.call(t, "DELETE", path, nil, 403)
	f.admin.csrf = csrf
	f.admin.call(t, "DELETE", "/admin/channels/missing", nil, 404)
	for _, state := range []string{"queued", "running", "lease", "cancel", "reserved", "review"} {
		t.Run(state, func(t *testing.T) {
			row := model.Task{ID: kernel.NewID(), UserID: user.userID, ChannelModelID: item.ID, Type: "canvas_audio", Status: model.TaskStatusFailed}
			switch state {
			case "queued", "running":
				row.Status = model.TaskStatus(state)
			case "lease":
				expires := time.Now().UTC().Add(time.Minute)
				row.LeaseOwner, row.LeaseExpiresAt = "test-worker", &expires
			case "cancel":
				row.Status, row.ProviderCancelStatus = model.TaskStatusCancelled, model.ProviderCancelStatusRequested
			}
			if err := f.runtime.db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				f.runtime.db.Where("task_id = ?", row.ID).Delete(&model.CreditReservation{})
				f.runtime.db.Delete(&row)
			})
			if state == "reserved" || state == "review" {
				credit := model.CreditReservation{ID: kernel.NewID(), UserID: user.userID, TaskID: row.ID, QuoteID: kernel.NewID(), ModelID: item.ID, State: state}
				if err := f.runtime.db.Create(&credit).Error; err != nil {
					t.Fatal(err)
				}
			}
			before, err := repository.NewPlatform(f.runtime.db).AdminSystemChannel(channel.ID)
			if err != nil {
				t.Fatal(err)
			}
			result := f.admin.call(t, "DELETE", path, nil, 409)
			after, err := repository.NewPlatform(f.runtime.db).AdminSystemChannel(channel.ID)
			if result["reason"] != "channel_in_use" || err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("占用未阻止删除或拒绝操作后渠道已改变")
			}
		})
	}
	f.admin.call(t, "PUT", path, map[string]any{"enabled": false}, 200)
	f.admin.call(t, "DELETE", path, nil, 200)
	empty := f.createChannel(t, "空渠道删除验收")
	f.admin.call(t, "DELETE", "/admin/channels/"+empty.ID, nil, 200)
}

func TestPlatformChannelDeleteRollsBackEveryMutation(t *testing.T) {
	f := newCatalogFixture(t)
	channel := f.createChannel(t, "删除回滚验收", catalogModel("rollback-image", "image", "openai-image", catalogVariant(nil, catalogAmount(3))))
	item := f.models(t, channel.ID, "").Items[0]
	f.admin.call(t, "PUT", "/admin/prices", map[string]any{"modelId": item.ID, "variantId": item.Variants[0].ID, "unitCredits": 3, "published": true}, 200)
	for _, table := range []string{"channel_model_variants", "model_credit_prices", "channel_models", "model_channels", "platform_audits"} {
		t.Run(table, func(t *testing.T) {
			before := catalogRows(t, f.runtime)
			repo := repository.NewPlatform(f.runtime.db)
			beforeChannel, _ := repo.AdminSystemChannel(channel.ID)
			beforeModels := f.models(t, channel.ID, "")
			callback := "channel_delete_test:" + table
			reject := func(tx *gorm.DB) {
				if tx.Statement.Table == table {
					tx.AddError(errors.New("注入删除事务故障"))
				}
			}
			var err error
			switch table {
			case "platform_audits":
				err = f.runtime.db.Callback().Create().Before("gorm:create").Register(callback, reject)
				t.Cleanup(func() { _ = f.runtime.db.Callback().Create().Remove(callback) })
			case "model_credit_prices":
				err = f.runtime.db.Callback().Update().Before("gorm:update").Register(callback, reject)
				t.Cleanup(func() { _ = f.runtime.db.Callback().Update().Remove(callback) })
			default:
				err = f.runtime.db.Callback().Delete().Before("gorm:delete").Register(callback, reject)
				t.Cleanup(func() { _ = f.runtime.db.Callback().Delete().Remove(callback) })
			}
			if err != nil {
				t.Fatal(err)
			}
			f.admin.call(t, "DELETE", "/admin/channels/"+channel.ID, nil, 500)
			afterChannel, err := repo.AdminSystemChannel(channel.ID)
			if err != nil || !reflect.DeepEqual(before, catalogRows(t, f.runtime)) || !reflect.DeepEqual(beforeChannel, afterChannel) || !reflect.DeepEqual(beforeModels, f.models(t, channel.ID, "")) {
				t.Fatal("删除故障后没有整体回滚渠道、凭据、模型、规格、价格及审计")
			}
		})
	}
	f.admin.call(t, "DELETE", "/admin/channels/"+channel.ID, nil, 200)
}
