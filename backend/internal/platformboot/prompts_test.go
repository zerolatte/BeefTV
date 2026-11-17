//go:build platform

package platformboot

import (
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/prompts"
)

func TestPlatformPromptPreferencesRemainWorkspaceScoped(t *testing.T) {
	fixture := newPlatformFixture(t)
	browsers := []*platformBrowser{fixture.login(t, fixture.admin.Username, false)}
	for _, username := range []string{"alice", "bravo"} {
		account, err := fixture.runtime.Auth.CreateAccount(t.Context(), fixture.admin.ID, platformAccountInput(username))
		if err != nil {
			t.Fatal(err)
		}
		browsers = append(browsers, fixture.login(t, account.Username, true))
	}
	const operation = prompts.OperationShortDramaOutline
	var original model.PromptTemplate
	if err := fixture.runtime.db.Where("operation = ? AND enabled = ?", operation, true).Take(&original).Error; err != nil {
		t.Fatal("平台未初始化默认提示词", err)
	}
	for _, browser := range browsers {
		response := browser.request(t, "PATCH", "/settings/prompt-templates/"+operation, map[string]any{
			"mode": "append", "content": browser.account.Username, "userId": fixture.admin.WorkspaceID,
		}, 200, nil)
		stored := decodePlatformData[struct{ Customization model.UserPromptCustomization }](t, response).Customization
		if stored.UserID != browser.account.WorkspaceID {
			t.Fatal("正文身份覆盖会话工作区")
		}
	}
	assertPreference := func(browser *platformBrowser, expected string) {
		t.Helper()
		response := browser.request(t, "GET", "/settings/prompt-templates", nil, 200, nil)
		preferences := decodePlatformData[struct {
			Preferences []prompts.UserPromptPreference
		}](t, response).Preferences
		for _, preference := range preferences {
			if preference.Definition.Operation != operation {
				continue
			}
			if preference.Template == nil || preference.Template.Content != original.Content {
				t.Fatal("用户定制覆盖了全局模板")
			}
			if expected == "" {
				if preference.Customization != nil {
					t.Fatal("重置后仍返回定制")
				}
			} else if preference.Customization == nil || preference.Customization.Content != expected || preference.Customization.UserID != browser.account.WorkspaceID {
				t.Fatal("读取了其他工作区定制")
			}
			return
		}
		t.Fatal("缺少已支持的提示词操作")
	}
	for _, browser := range browsers {
		assertPreference(browser, browser.account.Username)
	}
	browsers[0].request(t, "DELETE", "/settings/prompt-templates/"+operation, nil, 200, nil)
	assertPreference(browsers[0], "")
	assertPreference(browsers[1], browsers[1].account.Username)
	assertPreference(browsers[2], browsers[2].account.Username)
	var current model.PromptTemplate
	if err := fixture.runtime.db.First(&current, "id = ?", original.ID).Error; err != nil || current.Content != original.Content || current.UpdatedAt != original.UpdatedAt {
		t.Fatal("定制修改或重置写入了全局模板", err)
	}
}
