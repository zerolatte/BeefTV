package repository

import (
	"fmt"
	"testing"

	"infinite-canvas/backend/internal/model"
)

func TestPlatformCatalogReadinessUsesActiveVariantsAndSQLFilters(t *testing.T) {
	repo := creditRepository(t)
	profile := `{"version":1,"text":{"streaming":true,"thinking":false,"references":{"promptMaxChars":32000}}}`
	if err := repo.db.Model(&model.ChannelModel{}).Where("id = ?", "model").Updates(map[string]any{"capability": "text", "protocol": "chat-completion", "capability_config_json": profile}).Error; err != nil {
		t.Fatal(err)
	}
	protocols := map[string][]string{"text": {"chat-completion"}, "audio": {"openai-audio"}}
	check := func(state string, count int64) {
		t.Helper()
		rows, total, err := repo.PlatformModels("channel", PlatformCatalogQuery{Page: 1, PageSize: 20, Status: state}, protocols)
		if err != nil || total != count || int64(len(rows)) != count {
			t.Fatalf("状态 %s: rows=%d total=%d err=%v", state, len(rows), total, err)
		}
	}
	check("available", 1)
	variants := []model.ChannelModelVariant{{ID: "variant-1", ChannelModelID: "model", SelectorKey: "{}", SelectorJSON: "{}", Enabled: true}, {ID: "variant-2", ChannelModelID: "model", SelectorKey: `{"operation":"other"}`, SelectorJSON: `{"operation":"other"}`, Enabled: true}}
	if err := repo.db.Create(&variants).Error; err != nil {
		t.Fatal(err)
	}
	check("unpublished", 1)
	if err := repo.db.Create(&model.ModelCreditPrice{ID: "specific-price", ChannelModelID: "model", VariantID: "variant-1", UnitCredits: 0, Version: 1, Published: true}).Error; err != nil {
		t.Fatal(err)
	}
	check("partially_published", 1)
	channels, total, err := repo.PlatformChannels(PlatformCatalogQuery{Page: 1, PageSize: 10}, protocols)
	if err != nil || total != 1 || channels[0].AvailableModels != 1 || channels[0].TotalModels != 1 {
		t.Fatalf("渠道统计错误: %#v total=%d err=%v", channels, total, err)
	}
	if err := repo.db.Delete(&variants[0]).Error; err != nil {
		t.Fatal(err)
	}
	check("unpublished", 1)
	if err := repo.db.Model(&variants[1]).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	check("incomplete", 1)
	if err := repo.db.Model(&model.ModelChannel{}).Where("id = ?", "channel").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	check("channel_disabled", 1)
}

func TestPlatformCatalogFiltersBeforePaginationAndAcceptsAudioWithoutJSON(t *testing.T) {
	repo := creditRepository(t)
	for i := 0; i < 25; i++ {
		row := model.ChannelModel{ID: fmt.Sprintf("audio-%02d", i), ChannelID: "channel", ModelKey: fmt.Sprintf("audio-%02d", i), DisplayName: "音频", Capability: "audio", Protocol: "openai-audio", Enabled: true}
		if err := repo.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	protocols := map[string][]string{"audio": {"openai-audio"}}
	rows, total, err := repo.PlatformModels("channel", PlatformCatalogQuery{Page: 2, PageSize: 20, Capability: "audio", Status: "unpublished"}, protocols)
	if err != nil || total != 25 || len(rows) != 5 {
		t.Fatalf("分页前筛选失败: %d/%d %v", len(rows), total, err)
	}
	rows, total, err = repo.PlatformModels("channel", PlatformCatalogQuery{Page: 1, PageSize: 20, Keyword: "audio-24", Capability: "audio"}, protocols)
	if err != nil || total != 1 || len(rows) != 1 || rows[0].ReadinessState != "unpublished" {
		t.Fatalf("音频空能力查询失败: %#v %d %v", rows, total, err)
	}
}

func TestPlatformCatalogVariantIdentityPreservesPrice(t *testing.T) {
	repo := creditRepository(t)
	row, err := repo.ChannelModel("model")
	if err != nil {
		t.Fatal(err)
	}
	variants := []model.ChannelModelVariant{{ID: "stable", SelectorKey: "{}", SelectorJSON: "{}", ProviderModelKey: "old", Enabled: true}}
	if err := repo.SaveChannelModelWithVariants(row, variants); err != nil {
		t.Fatal(err)
	}
	price := model.ModelCreditPrice{ID: "stable-price", ChannelModelID: row.ID, VariantID: "stable", UnitCredits: 7, Version: 1, Published: true}
	if err := repo.db.Create(&price).Error; err != nil {
		t.Fatal(err)
	}
	variants[0].ID, variants[0].ProviderModelKey = "discarded", "new"
	if err := repo.SaveChannelModelWithVariants(row, variants); err != nil {
		t.Fatal(err)
	}
	if variants[0].ID != "stable" {
		t.Fatal("相同规格没有保留 ID")
	}
	saved, err := repo.CreditPrice(row.ID, variants[0].ID)
	if err != nil || saved.ID != price.ID || !saved.Published || saved.UnitCredits != 7 {
		t.Fatalf("价格关联丢失: %#v %v", saved, err)
	}
}

func TestPlatformCatalogVideoOptionalPromptLimitsRemainAvailable(t *testing.T) {
	repo := creditRepository(t)
	protocols := map[string][]string{"video": {"openai-videos"}}
	for _, test := range []struct {
		name, references, state string
	}{
		{"由上游校验", `{"promptMaxChars":0,"promptMaxUtf16Units":0}`, "available"},
		{"仅限制UTF16", `{"promptMaxChars":0,"promptMaxUtf16Units":2048}`, "available"},
		{"仅限制字符", `{"promptMaxChars":4096,"promptMaxUtf16Units":0}`, "available"},
		{"旧配置未声明UTF16", `{"promptMaxChars":0}`, "available"},
		{"负字符上限", `{"promptMaxChars":-1,"promptMaxUtf16Units":0}`, "incomplete"},
		{"负UTF16上限", `{"promptMaxChars":0,"promptMaxUtf16Units":-1}`, "incomplete"},
		{"超大字符上限", `{"promptMaxChars":1000001,"promptMaxUtf16Units":0}`, "incomplete"},
		{"缺少引用合同", `{}`, "incomplete"},
	} {
		t.Run(test.name, func(t *testing.T) {
			profile := fmt.Sprintf(`{"version":1,"video":{"references":%s}}`, test.references)
			if err := repo.db.Model(&model.ChannelModel{}).Where("id = ?", "model").Updates(map[string]any{"capability": "video", "protocol": "openai-videos", "capability_config_json": profile}).Error; err != nil {
				t.Fatal(err)
			}
			rows, total, err := repo.PlatformModels("channel", PlatformCatalogQuery{Page: 1, PageSize: 20, Status: test.state}, protocols)
			if err != nil || total != 1 || len(rows) != 1 {
				t.Fatalf("视频提示词限制被误判: rows=%v total=%d err=%v", rows, total, err)
			}
			channels, _, err := repo.PlatformChannels(PlatformCatalogQuery{Page: 1, PageSize: 10}, protocols)
			want := int64(0)
			if test.state == "available" {
				want = 1
			}
			if err != nil || len(channels) != 1 || channels[0].AvailableModels != want {
				t.Fatalf("渠道统计与视频合法合同不一致: channels=%v err=%v", channels, err)
			}
		})
	}
}

func TestPlatformCatalogImageAllowsZeroPromptLimitWhileTextRequiresPositive(t *testing.T) {
	repo := creditRepository(t)
	protocols := map[string][]string{"image": {"openai-image"}, "text": {"chat-completion"}}
	for _, capability := range []string{"image", "text"} {
		protocol, state := "openai-image", "available"
		if capability == "text" {
			protocol, state = "chat-completion", "incomplete"
		}
		profile := fmt.Sprintf(`{"version":1,"%s":{"references":{"promptMaxChars":0}}}`, capability)
		if err := repo.db.Model(&model.ChannelModel{}).Where("id = ?", "model").Updates(map[string]any{"capability": capability, "protocol": protocol, "capability_config_json": profile}).Error; err != nil {
			t.Fatal(err)
		}
		row, err := repo.PlatformModel("model", protocols)
		if err != nil || row.ReadinessState != state {
			t.Fatalf("%s 零提示词限制判断错误: %+v %v", capability, row, err)
		}
	}
}
