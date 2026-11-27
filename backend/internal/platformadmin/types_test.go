package platformadmin

import (
	"encoding/json"
	"testing"
)

func TestModelRequestRejectsUnsupportedFieldsWithoutDroppingThem(t *testing.T) {
	for _, raw := range []string{
		`{"modelKey":"m","capabilityConfig":{"text":{"unknownSetting":true}}}`,
		`{"modelKey":"m","variants":[{"selector":{"videoSeconds":5}}]}`,
		`{"modelKey":"m","variants":[{"price":{"unitCredits":2,"published":true}}]}`,
		`{"modelKey":"m","variants":[{"price":{}}]}`,
	} {
		var req ModelRequest
		if err := json.Unmarshal([]byte(raw), &req); err == nil {
			t.Fatalf("不支持的模型字段被静默忽略: %s", raw)
		}
	}
	var req ModelRequest
	if err := json.Unmarshal([]byte(`{"modelKey":"m","variants":[{"selector":{},"price":{"unitCredits":0}}]}`), &req); err != nil || req.Variants[0].Price == nil || req.Variants[0].Price.UnitCredits != 0 {
		t.Fatalf("零积分价格未保留: %#v %v", req, err)
	}
}
