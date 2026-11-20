package credits

import (
	"fmt"
	"infinite-canvas/backend/internal/kernel"
	"strings"
)

// 生成配置只允许声明的创作参数，执行协议和地址始终由服务端目录重建。
func validateCreativeInput(input, config map[string]any) error {
	allowedConfig := strings.Fields("channelId model channelModelKey variantId providerModelKey apiFormat interfaceType capabilityConfig imageCapabilitiesVersion videoCapabilitiesVersion size quality transparentBackground seedreamMode seedreamMaxImages seedreamOutputFormat seedreamOptimize seedreamWatermark count videoSeconds vquality videoGenerateAudio videoWatermark videoDraft videoReturnLastFrame videoArkPrivateAssetUpload audioVoice audioFormat audioSpeed audioPitch audioVolume audioInstructions systemPrompt")
	allowed := map[string]bool{}
	for _, key := range allowedConfig {
		allowed[key] = true
	}
	for key := range config {
		if !allowed[key] {
			return kernel.BadAuthRequest("不支持的模型参数：" + key)
		}
	}
	if metadata, ok := input["metadata"].(map[string]any); ok {
		for _, key := range []string{"providerOptions", "executor", "script", "workflow", "textReplay", "sourceURL", "baseUrl", "apiKey"} {
			if _, exists := metadata[key]; exists {
				return kernel.BadAuthRequest("不支持的生成上下文参数：" + key)
			}
		}
	}
	if options, ok := input["textOptions"].(map[string]any); ok {
		for key, value := range options {
			if key != "stream" && key != "thinking" {
				return kernel.BadAuthRequest("不支持的文本参数")
			}
			if _, ok := value.(bool); !ok {
				return kernel.BadAuthRequest("文本参数必须是布尔值")
			}
		}
	}
	return nil
}

func imageUnits(config map[string]any, units int64) (int64, error) {
	// 连续组图使用声明的最大产出数预占，避免单张报价承载多张生成。
	if fmt.Sprint(config["seedreamMode"]) == "auto" {
		if config["seedreamMaxImages"] == nil || fmt.Sprint(config["seedreamMaxImages"]) == "" {
			return 0, kernel.BadAuthRequest("连续组图必须指定最大生成张数，以确认积分报价")
		}
		copyConfig := map[string]any{"count": config["seedreamMaxImages"]}
		maximum, err := unitsFor("image", copyConfig)
		if err != nil {
			return 0, err
		}
		if maximum > units {
			return maximum, nil
		}
	}
	return units, nil
}
