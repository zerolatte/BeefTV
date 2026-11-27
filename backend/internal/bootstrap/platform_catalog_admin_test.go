package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/credits"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/platformadmin"
)

func TestPlatformCatalogBundlePublicationAndVariantIdentity(t *testing.T) {
	f := newCatalogFixture(t)
	user := f.user(t)
	textRequest := catalogModel("sample-text", "text", "chat-completion", catalogVariant(nil, catalogAmount(4)))
	imageRequest := catalogModel("sample-image", "image", "openai-image", catalogVariant(map[string]string{"quality": "low"}, catalogAmount(2)), catalogVariant(map[string]string{"quality": "high"}, catalogAmount(0)))
	channel := f.createChannel(t, "组合保存验收", textRequest, imageRequest, catalogModel("sample-audio", "audio", "openai-audio"))
	if channel.ID == "" || !channel.Enabled || !channel.HasAPIKey || channel.APIKey != "" {
		t.Fatalf("渠道默认启用且凭据应隐藏: %+v", channel)
	}
	page := f.models(t, channel.ID, "")
	if page.Total != 3 || len(page.Items) != 3 || page.PageSize != 20 {
		t.Fatalf("组合模型未完整保存: %+v", page)
	}
	for _, item := range page.Items {
		if item.Readiness.State != "unpublished" {
			t.Fatalf("新模型必须未发布: %s %+v", item.ModelKey, item.Readiness)
		}
		for _, variant := range item.Variants {
			if variant.Price != nil && variant.Price.Published {
				t.Fatalf("新价格被自动发布: %+v", variant.Price)
			}
		}
	}
	// 默认规格条件必须作为空对象返回，不能因 omitempty 让可视化表格收到 undefined。
	rawPage := data(f.admin.call(t, "GET", "/admin/channels/"+channel.ID+"/models", nil, 200))
	for _, raw := range rawPage["items"].([]any) {
		for _, rawVariant := range raw.(map[string]any)["variants"].([]any) {
			if _, ok := rawVariant.(map[string]any)["selector"].(map[string]any); !ok {
				t.Fatalf("规格响应缺少条件对象: %+v", rawVariant)
			}
		}
	}
	text := catalogFindModel(t, page, "sample-text")
	image := catalogFindModel(t, page, "sample-image")
	audio := catalogFindModel(t, page, "sample-audio")
	low := catalogFindVariant(t, image, "quality", "low")
	high := catalogFindVariant(t, image, "quality", "high")
	if high.Price == nil || high.Price.UnitCredits != 0 || len(audio.Variants) != 1 || audio.Variants[0].Price != nil {
		t.Fatal("明确零积分与未填写价格未被区分")
	}
	if len(catalogUserModels(t, user)) != 0 {
		t.Fatal("未发布模型进入了用户目录")
	}
	publish := func(modelID string, variant platformadmin.VariantView) {
		t.Helper()
		f.admin.call(t, "PUT", "/admin/prices", map[string]any{"modelId": modelID, "variantId": variant.ID, "unitCredits": variant.Price.UnitCredits, "published": true}, 200)
	}
	publish(image.ID, low)
	image = catalogFindModel(t, f.models(t, channel.ID, ""), image.ModelKey)
	if image.Readiness.State != "partially_published" || !catalogUserModels(t, user)[image.ModelKey] {
		t.Fatalf("部分发布状态或用户目录不正确: %+v", image.Readiness)
	}
	publish(text.ID, text.Variants[0])
	publish(image.ID, high)
	image = catalogFindModel(t, f.models(t, channel.ID, ""), image.ModelKey)
	low = catalogFindVariant(t, image, "quality", "low")
	high = catalogFindVariant(t, image, "quality", "high")
	channels := catalogData[platformadmin.ChannelPage](t, f.admin.call(t, "GET", "/admin/channels", nil, 200))
	if image.Readiness.State != "available" || channels.Channels[0].TotalModels != 3 || channels.Channels[0].AvailableModels != 2 {
		t.Fatalf("发布统计不正确: %+v %+v", image.Readiness, channels.Channels)
	}

	// 条件规范化后仍是同一规格；改变显示名称、上游标识不应破坏价格关联。
	imageRequest.DisplayName = "更新后的图片名称"
	imageRequest.ProviderModelKey = "image-upstream-renamed"
	imageRequest.Variants = []platformadmin.VariantRequest{catalogVariant(map[string]string{"quality": " LOW "}, nil), catalogVariant(map[string]string{"quality": "high"}, catalogAmount(0))}
	updated := catalogData[platformadmin.ModelView](t, f.admin.call(t, "PUT", "/admin/channels/"+channel.ID+"/models/"+image.ID, imageRequest, 200))
	for _, previous := range []platformadmin.VariantView{low, high} {
		current := catalogFindVariant(t, updated, "quality", previous.Selector["quality"])
		if current.ID != previous.ID || current.Price == nil || current.Price.ID != previous.Price.ID || current.Price.Version != previous.Price.Version || !current.Price.Published {
			t.Fatalf("同规格编辑丢失价格或错误递增版本: 原=%+v 新=%+v", previous, current)
		}
	}
	imageRequest.Variants = []platformadmin.VariantRequest{catalogVariant(map[string]string{"quality": "medium"}, catalogAmount(8))}
	updated = catalogData[platformadmin.ModelView](t, f.admin.call(t, "PUT", "/admin/channels/"+channel.ID+"/models/"+image.ID, imageRequest, 200))
	if len(updated.Variants) != 1 || updated.Variants[0].ID == low.ID || updated.Variants[0].ID == high.ID || updated.Variants[0].Price.Published || updated.Readiness.State != "unpublished" {
		t.Fatalf("新规格错误沿用旧发布状态: %+v", updated)
	}
	if catalogUserModels(t, user)[image.ModelKey] {
		t.Fatal("软删除规格的旧价格仍使模型进入用户目录")
	}
	f.admin.call(t, "PUT", "/admin/channels/"+channel.ID, map[string]any{"enabled": false}, 200)
	if len(catalogUserModels(t, user)) != 0 || catalogFindModel(t, f.models(t, channel.ID, ""), text.ModelKey).Readiness.State != "channel_disabled" {
		t.Fatal("渠道停用未同步到模型状态和用户目录")
	}
	f.admin.call(t, "PUT", "/admin/channels/"+channel.ID, map[string]any{"enabled": true}, 200)
	if !catalogUserModels(t, user)[text.ModelKey] {
		t.Fatal("重新启用渠道丢失了已发布价格")
	}
	disabled := false
	textRequest.Variants[0].Enabled = &disabled
	f.admin.call(t, "PUT", "/admin/channels/"+channel.ID+"/models/"+text.ID, textRequest, 200)
	channels = catalogData[platformadmin.ChannelPage](t, f.admin.call(t, "GET", "/admin/channels", nil, 200))
	if channels.Channels[0].AvailableModels != 0 || len(catalogUserModels(t, user)) != 0 {
		t.Fatal("停用规格的旧价格仍被计入可用模型")
	}
}

func TestPlatformCatalogBundleRollsBackEveryWriteStage(t *testing.T) {
	f := newCatalogFixture(t)
	valid := catalogModel("atomic-text", "text", "chat-completion", catalogVariant(nil, catalogAmount(3)))
	request := platformadmin.ChannelRequest{ChannelRequest: modelcatalog.ChannelRequest{Name: "事务验收", BaseURL: "https://catalog.example.invalid", APIKey: "catalog-synthetic-key"}, InitialModels: []platformadmin.ModelRequest{valid}}
	before := catalogRows(t, f.runtime)
	invalid := valid
	invalid.ModelKey = "invalid-second"
	invalid.Protocol = "uninstalled-protocol"
	request.InitialModels = []platformadmin.ModelRequest{valid, invalid}
	f.admin.call(t, "POST", "/admin/channels", request, 400)
	if after := catalogRows(t, f.runtime); !reflect.DeepEqual(after, before) {
		t.Fatalf("第二个模型校验失败仍有部分落库: 前=%v 后=%v", before, after)
	}
	request.InitialModels = []platformadmin.ModelRequest{valid}
	for _, table := range []string{"model_channels", "channel_models", "channel_model_variants", "model_credit_prices", "platform_audits"} {
		t.Run(table, func(t *testing.T) {
			const callback = "catalog_test:reject_create"
			if err := f.runtime.db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == table {
					tx.AddError(errors.New("目录验收注入保存失败"))
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = f.runtime.db.Callback().Create().Remove(callback) })
			f.admin.call(t, "POST", "/admin/channels", request, 500)
			if after := catalogRows(t, f.runtime); !reflect.DeepEqual(after, before) {
				t.Fatalf("%s 失败没有完整回滚: 前=%v 后=%v", table, before, after)
			}
		})
	}
	channel := f.createChannel(t, "已有渠道", valid)
	before = catalogRows(t, f.runtime)
	duplicate := catalogModel("duplicate-selector", "image", "openai-image", catalogVariant(map[string]string{"quality": "low"}, nil), catalogVariant(map[string]string{"quality": " LOW "}, nil))
	f.admin.call(t, "POST", "/admin/channels/"+channel.ID+"/models/batch", map[string]any{"models": []platformadmin.ModelRequest{catalogModel("would-be-first", "audio", "openai-audio"), duplicate}}, 400)
	if after := catalogRows(t, f.runtime); !reflect.DeepEqual(after, before) {
		t.Fatalf("批量添加验证失败留下部分模型: 前=%v 后=%v", before, after)
	}
	// 同一个渠道内的模型标识不能重复，也不能覆盖原模型及价格。
	f.admin.call(t, "POST", "/admin/channels/"+channel.ID+"/models/batch", map[string]any{"models": []platformadmin.ModelRequest{valid}}, 400)
	if after := catalogRows(t, f.runtime); !reflect.DeepEqual(after, before) {
		t.Fatalf("重复添加改变了既有模型: 前=%v 后=%v", before, after)
	}
}

func TestPlatformCatalogPaginationAndUnconfiguredModels(t *testing.T) {
	f := newCatalogFixture(t)
	for i := 0; i < 12; i++ {
		channel := f.createChannel(t, fmt.Sprintf("分页渠道%02d", i))
		if i%2 == 0 {
			f.admin.call(t, "PUT", "/admin/channels/"+channel.ID, map[string]any{"enabled": false}, 200)
		}
	}
	page := catalogData[platformadmin.ChannelPage](t, f.admin.call(t, "GET", "/admin/channels?page=2&pageSize=10", nil, 200))
	if page.Total != 12 || len(page.Channels) != 2 || page.Page != 2 || page.PageSize != 10 {
		t.Fatalf("渠道分页错误: %+v", page)
	}
	page = catalogData[platformadmin.ChannelPage](t, f.admin.call(t, "GET", "/admin/channels?q=11&status=enabled&pageSize=10", nil, 200))
	if page.Total != 1 || len(page.Channels) != 1 || page.Channels[0].Name != "分页渠道11" {
		t.Fatalf("渠道搜索或启停过滤未先于分页: %+v", page)
	}
	channelID := page.Channels[0].ID
	requests := make([]platformadmin.ModelRequest, 0, 23)
	for i := 0; i < 23; i++ {
		requests = append(requests, catalogModel(fmt.Sprintf("paged-audio-%02d", i), "audio", "openai-audio"))
	}
	result := catalogData[platformadmin.ModelPage](t, f.admin.call(t, "POST", "/admin/channels/"+channelID+"/models/batch", map[string]any{"models": requests}, 200))
	if len(result.Items) != 23 {
		t.Fatalf("模型批量创建不完整: %d", len(result.Items))
	}
	modelPage := f.models(t, channelID, "?page=2&pageSize=20&capability=audio")
	if modelPage.Total != 23 || len(modelPage.Items) != 3 || modelPage.Page != 2 {
		t.Fatalf("音频模型分页错误: %+v", modelPage)
	}
	modelPage = f.models(t, channelID, "?q=22&capability=audio&status=unpublished&pageSize=20")
	if modelPage.Total != 1 || len(modelPage.Items) != 1 || modelPage.Items[0].ModelKey != "paged-audio-22" {
		t.Fatalf("模型过滤未先于分页: %+v", modelPage)
	}
	// 旧目录导入草稿没有能力 JSON，也没有请求协议，列表必须仍然可读。
	draft := model.ChannelModel{ID: kernel.NewID(), ChannelID: channelID, ModelKey: "unconfigured-import", Enabled: true}
	if err := f.runtime.db.Create(&draft).Error; err != nil {
		t.Fatal(err)
	}
	modelPage = f.models(t, channelID, "?q=unconfigured&status=incomplete")
	if modelPage.Total != 1 || modelPage.Items[0].Readiness.State != "incomplete" {
		t.Fatalf("待配置模型没有保留真实状态: %+v", modelPage)
	}
	f.admin.call(t, "PUT", "/admin/channels/"+channelID+"/models/"+draft.ID, map[string]any{"enabled": false}, 200)
	modelPage = f.models(t, channelID, "?q=unconfigured&status=model_disabled")
	if modelPage.Total != 1 || modelPage.Items[0].Enabled || modelPage.Items[0].Protocol != "" {
		t.Fatalf("待配置模型无法独立停用或被改写配置: %+v", modelPage)
	}
	t.Run("待配置模型启停审计失败回滚", func(t *testing.T) {
		const callback = "catalog_test:reject_model_toggle_audit"
		before := catalogRows(t, f.runtime)
		if err := f.runtime.db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
			if tx.Statement.Table == "platform_audits" {
				tx.AddError(errors.New("模型启停审计失败"))
			}
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = f.runtime.db.Callback().Create().Remove(callback) })
		f.admin.call(t, "PUT", "/admin/channels/"+channelID+"/models/"+draft.ID, map[string]any{"enabled": true}, 500)
		current := f.models(t, channelID, "?q=unconfigured")
		if len(current.Items) != 1 || current.Items[0].Enabled || !reflect.DeepEqual(before, catalogRows(t, f.runtime)) {
			t.Fatal("模型启停审计失败后状态没有回滚")
		}
	})
	f.admin.call(t, "PUT", "/admin/channels/"+channelID+"/models/"+draft.ID, map[string]any{"enabled": true}, 200)
	if current := f.models(t, channelID, "?q=unconfigured"); len(current.Items) != 1 || !current.Items[0].Enabled || current.Items[0].Readiness.State != "incomplete" {
		t.Fatalf("待配置模型重新启用后错误伪装成可用: %+v", current)
	}
	f.admin.call(t, "GET", "/admin/channels/missing-channel/models", nil, 404)
}

func TestPlatformCatalogAdminPermissionsAndCapabilityContract(t *testing.T) {
	f := newCatalogFixture(t)
	user := f.user(t)
	channel := f.createChannel(t, "权限验收")
	for _, endpoint := range []struct{ method, path string }{
		{"GET", "/admin/protocols"}, {"POST", "/admin/model-capabilities"}, {"POST", "/admin/channels/probe"},
		{"POST", "/admin/channels"}, {"POST", "/admin/channels/" + channel.ID + "/models/batch"}, {"POST", "/admin/prices/batch"},
	} {
		user.call(t, endpoint.method, endpoint.path, map[string]any{}, 403)
	}
	csrf := f.admin.csrf
	f.admin.csrf = "invalid"
	f.admin.call(t, "POST", "/admin/channels/probe", map[string]any{"channelId": channel.ID}, 403)
	f.admin.csrf = csrf
	providers := data(f.admin.call(t, "GET", "/admin/protocols?capability=text", nil, 200))["providers"].([]any)
	if len(providers) == 0 {
		t.Fatal("管理员协议目录为空")
	}
	found := false
	for _, raw := range providers {
		provider := raw.(map[string]any)
		if provider["id"] == "chat-completion" {
			found = true
		}
	}
	if !found {
		t.Fatal("协议目录缺少当前安装的文本协议")
	}
	profile := data(f.admin.call(t, "POST", "/admin/model-capabilities", map[string]any{"capability": "text", "protocol": "chat-completion", "model": "neutral-model"}, 200))
	if profile["text"] == nil {
		t.Fatalf("未从服务端合同返回文本默认能力: %+v", profile)
	}
	audio := f.admin.call(t, "POST", "/admin/model-capabilities", map[string]any{"capability": "audio", "protocol": "openai-audio", "model": "neutral-audio"}, 200)
	if audio["data"] != nil {
		t.Fatalf("音频被添加了不存在的可视化能力字段: %+v", audio)
	}
	f.admin.call(t, "POST", "/admin/model-capabilities", map[string]any{"capability": "image", "protocol": "chat-completion", "model": "neutral-model"}, 400)
	rows := catalogRows(t, f.runtime)
	if rows["channel_models"] != 0 || rows["tasks"] != 0 {
		t.Fatalf("读取合同意外创建模型或任务: %+v", rows)
	}
	var audits []model.PlatformAudit
	if err := f.runtime.db.Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	for _, audit := range audits {
		if strings.Contains(audit.Reason, "catalog-synthetic-key") {
			t.Fatal("审计正文泄露模型凭据")
		}
	}
}

func TestPlatformCatalogChangesInvalidateQuotesAndKeepAcceptedPrices(t *testing.T) {
	f := newCatalogFixture(t)
	user := f.user(t)
	modelRequest := catalogModel("quote-text", "text", "chat-completion", catalogVariant(nil, catalogAmount(4)))
	channel := f.createChannel(t, "报价验收", modelRequest)
	item := catalogFindModel(t, f.models(t, channel.ID, ""), modelRequest.ModelKey)
	f.admin.call(t, "POST", "/admin/accounts/"+user.userID+"/credits", map[string]any{"amount": 100, "operationId": "catalog-quote-credit", "reason": "目录报价行为测试"}, 200)
	f.admin.call(t, "PUT", "/admin/prices", map[string]any{"modelId": item.ID, "variantId": item.Variants[0].ID, "unitCredits": 4, "published": true}, 200)
	request := map[string]any{"type": "canvas_text", "operation": "text", "prompt": "合成测试", "model": item.ModelKey,
		"input": map[string]any{"mode": "text", "prompt": "合成测试", "config": map[string]any{"channelId": channel.ID, "model": item.ModelKey},
			"metadata": map[string]any{"clientOperationId": "catalog-quote-operation"}}}
	quote := data(user.call(t, "POST", "/tasks/quote", request, 200))
	request["quoteId"] = quote["id"]
	modelRequest.Variants[0].Price.UnitCredits = 8
	f.admin.call(t, "PUT", "/admin/channels/"+channel.ID+"/models/"+item.ID, modelRequest, 200)
	response := user.call(t, "POST", "/tasks", request, 409)
	if response["reason"] != "price_changed" {
		t.Fatalf("组合编辑调价没有使旧报价失效: %+v", response)
	}
	quote = data(user.call(t, "POST", "/tasks/quote", request, 200))
	request["quoteId"] = quote["id"]
	f.admin.call(t, "PUT", "/admin/channels/"+channel.ID, map[string]any{"enabled": false}, 200)
	status, _, err := catalogContextCall(user, context.Background(), "POST", "/tasks", request)
	if err != nil || status == 200 {
		t.Fatalf("已停用渠道仍能使用旧报价创建任务: status=%d err=%v", status, err)
	}
	f.admin.call(t, "PUT", "/admin/channels/"+channel.ID, map[string]any{"enabled": true}, 200)
	response = user.call(t, "POST", "/tasks", request, 409)
	if response["reason"] != "model_changed" {
		t.Fatalf("渠道启停没有使旧报价失效: %+v", response)
	}
	counts := catalogRows(t, f.runtime)
	if counts["tasks"] != 0 || counts["credit_reservations"] != 0 {
		t.Fatalf("失效报价留下任务或冻结账目: %+v", counts)
	}
	quote = data(user.call(t, "POST", "/tasks/quote", request, 200))
	request["quoteId"] = quote["id"]
	accepted := data(user.call(t, "POST", "/tasks", request, 200))
	var reservation model.CreditReservation
	if err := f.runtime.db.First(&reservation, "task_id = ?", accepted["id"]).Error; err != nil {
		t.Fatal(err)
	}
	modelRequest.Variants[0].Price.UnitCredits = 13
	f.admin.call(t, "PUT", "/admin/channels/"+channel.ID+"/models/"+item.ID, modelRequest, 200)
	var after model.CreditReservation
	if err := f.runtime.db.First(&after, "id = ?", reservation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reservation.UnitCredits != 8 || reservation.Reserved != 8 || !reflect.DeepEqual(reservation, after) {
		t.Fatalf("已受理任务的价格快照被后续调价修改: 原=%+v 新=%+v", reservation, after)
	}
	account := data(user.call(t, "GET", "/credits", nil, 200))
	if account["available"] != float64(92) || account["frozen"] != float64(8) {
		t.Fatalf("旧报价拒绝或后续调价改变了冻结金额: %+v", account)
	}
}

func TestPlatformCatalogPriceBatchPublishesAtomically(t *testing.T) {
	f := newCatalogFixture(t)
	channel := f.createChannel(t, "批量发布验收", catalogModel("batch-image", "image", "openai-image",
		catalogVariant(map[string]string{"quality": "low"}, catalogAmount(2)), catalogVariant(map[string]string{"quality": "high"}, catalogAmount(5))))
	item := catalogFindModel(t, f.models(t, channel.ID, ""), "batch-image")
	low, high := catalogFindVariant(t, item, "quality", "low"), catalogFindVariant(t, item, "quality", "high")
	requests := []credits.PriceRequest{{ModelID: item.ID, VariantID: low.ID, UnitCredits: 3, Published: true}, {ModelID: item.ID, VariantID: high.ID, UnitCredits: 6, Published: true}}
	before := catalogRows(t, f.runtime)
	invalid := append([]credits.PriceRequest(nil), requests...)
	invalid[1].VariantID = "missing-variant"
	f.admin.call(t, "POST", "/admin/prices/batch", map[string]any{"prices": invalid}, 400)
	if current := catalogFindModel(t, f.models(t, channel.ID, ""), item.ModelKey); !reflect.DeepEqual(item, current) || !reflect.DeepEqual(before, catalogRows(t, f.runtime)) {
		t.Fatal("第二项价格无效时，前一项仍被改价或发布")
	}
	t.Run("第二条审计失败回滚", func(t *testing.T) {
		const callback = "catalog_test:reject_second_price_audit"
		audits := 0
		if err := f.runtime.db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
			if tx.Statement.Table == "platform_audits" {
				audits++
				if audits == 2 {
					tx.AddError(errors.New("批量价格第二项审计失败"))
				}
			}
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = f.runtime.db.Callback().Create().Remove(callback) })
		f.admin.call(t, "POST", "/admin/prices/batch", map[string]any{"prices": requests}, 500)
		if audits != 2 {
			t.Fatalf("未命中第二条价格审计故障: %d", audits)
		}
		if current := catalogFindModel(t, f.models(t, channel.ID, ""), item.ModelKey); !reflect.DeepEqual(item, current) || !reflect.DeepEqual(before, catalogRows(t, f.runtime)) {
			t.Fatal("批量发布后项保存失败未回滚已写入价格与审计")
		}
	})
	result := data(f.admin.call(t, "POST", "/admin/prices/batch", map[string]any{"prices": requests}, 200))
	if len(result["items"].([]any)) != 2 {
		t.Fatalf("批量发布未返回最终价格: %+v", result)
	}
	current := catalogFindModel(t, f.models(t, channel.ID, ""), item.ModelKey)
	if current.Readiness.State != "available" || !catalogFindVariant(t, current, "quality", "low").Price.Published || catalogFindVariant(t, current, "quality", "high").Price.UnitCredits != 6 {
		t.Fatalf("批量发布后模型仍不可用: %+v", current)
	}
	// 同值重放不改变版本，避免无实质变动也使待提交报价失效。
	f.admin.call(t, "POST", "/admin/prices/batch", map[string]any{"prices": requests}, 200)
	if after := catalogFindModel(t, f.models(t, channel.ID, ""), item.ModelKey); !reflect.DeepEqual(current, after) {
		t.Fatal("同值批量发布错误增加了价格版本或改变模型")
	}
}

func TestPlatformCatalogPublishedVideoWithUpstreamPromptLimitsIsUsable(t *testing.T) {
	f := newCatalogFixture(t)
	request := catalogModel("neutral-video", "video", "openai-videos", catalogVariant(nil, catalogAmount(6)))
	if request.CapabilityConfig == nil || request.CapabilityConfig.Video == nil {
		t.Fatal("当前视频协议未返回能力合同")
	}
	request.CapabilityConfig.Video.References.PromptMaxChars = 0
	for _, utf16Limit := range []int{0, 2048} {
		request.ModelKey = fmt.Sprintf("neutral-video-%d", utf16Limit)
		request.CapabilityConfig.Video.References.PromptMaxUTF16Units = utf16Limit
		channel := f.createChannel(t, fmt.Sprintf("视频提示词限制%d", utf16Limit), request)
		item := catalogFindModel(t, f.models(t, channel.ID, ""), request.ModelKey)
		if item.Readiness.State != "unpublished" {
			t.Fatalf("合法视频合同错误标为待配置: %+v", item.Readiness)
		}
		f.admin.call(t, "PUT", "/admin/prices", map[string]any{"modelId": item.ID, "variantId": item.Variants[0].ID, "unitCredits": 6, "published": true}, 200)
		if item = catalogFindModel(t, f.models(t, channel.ID, ""), request.ModelKey); item.Readiness.State != "available" || !catalogUserModels(t, f.admin)[item.ModelKey] {
			t.Fatalf("合法已发布视频没有进入可用目录: %+v", item.Readiness)
		}
	}
}

func TestPlatformCatalogPublishedImageWithUpstreamPromptLimitIsUsable(t *testing.T) {
	f := newCatalogFixture(t)
	request := catalogModel("neutral-image", "image", "openai-image", catalogVariant(nil, catalogAmount(3)))
	request.CapabilityConfig.Image.References.PromptMaxChars = 0
	channel := f.createChannel(t, "图片提示词限制", request)
	item := catalogFindModel(t, f.models(t, channel.ID, ""), request.ModelKey)
	if item.Readiness.State != "unpublished" {
		t.Fatalf("合法图片零提示词限制错误标为待配置: %+v", item.Readiness)
	}
	f.admin.call(t, "PUT", "/admin/prices", map[string]any{"modelId": item.ID, "variantId": item.Variants[0].ID, "unitCredits": 3, "published": true}, 200)
	if item = catalogFindModel(t, f.models(t, channel.ID, ""), request.ModelKey); item.Readiness.State != "available" || !catalogUserModels(t, f.admin)[item.ModelKey] {
		t.Fatalf("合法已发布图片没有进入可用目录: %+v", item.Readiness)
	}
}
