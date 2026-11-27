package task

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/generation"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/mediatools"
	"infinite-canvas/backend/internal/outbound"
)

func generationArtifactDownloadOptions(deps DeliveryMediaDeps, userID, identity, kind, rawURL string) (outbound.ResumableOptions, error) {
	limit, err := deps.MaxBytes()
	if err != nil {
		return outbound.ResumableOptions{}, err
	}
	headers, err := ImageDownloadHeaders(deps.Repo, deps.DecryptTaskInput, deps.DecryptSetting, deps.ResolveConfig, deps.ProtocolRegistry, userID, identity, kind, rawURL)
	if err != nil {
		return outbound.ResumableOptions{}, err
	}
	return outbound.ResumableOptions{PartialPath: ArtifactDownloadPartialPath(deps.DataDir, userID, identity), Headers: headers, MaxBytes: limit + 1,
		OnProgress: func(update outbound.ResumableProgress) {
			if deps.Progress != nil {
				deps.Progress.Record(identity, DeliveryProgressFromOutbound(identity, update))
			}
		}}, nil
}

// 平台生成 URL 流式落盘后直接交给资源服务；仅读取有界文件头，视频和音频事实由文件探测。
func openStreamGenerationArtifact(ctx context.Context, deps DeliveryMediaDeps, userID, identity, kind, rawURL string) (asset.RecoveredArtifact, func(), error) {
	if _, err := outbound.ValidateOutboundURLContext(ctx, rawURL); err != nil {
		return asset.RecoveredArtifact{}, nil, err
	}
	options, err := generationArtifactDownloadOptions(deps, userID, identity, kind, rawURL)
	if err != nil {
		return asset.RecoveredArtifact{}, nil, err
	}
	options.ReturnFile = true
	result, err := outbound.DownloadResumable(ctx, rawURL, options)
	if deps.Progress != nil {
		deps.Progress.Clear(identity)
	}
	if errors.Is(err, outbound.ErrResourceTooLarge) {
		return asset.RecoveredArtifact{}, nil, kernel.BadAuthRequest("远程资源超过生成文件大小限制")
	}
	if err != nil {
		return asset.RecoveredArtifact{}, nil, err
	}
	file, err := os.Open(result.FilePath)
	if err != nil {
		return asset.RecoveredArtifact{}, nil, err
	}
	release := func() { _ = file.Close(); _ = os.Remove(result.FilePath); _ = os.Remove(result.FilePath + ".json") }
	header, err := io.ReadAll(io.LimitReader(file, imageHeaderDecodeLimit))
	if err != nil {
		return asset.RecoveredArtifact{}, release, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return asset.RecoveredArtifact{}, release, err
	}
	mimeType := generation.NormalizedMediaMIMEType(result.MimeType, header)
	kind = asset.NormalizeKind(kind, mimeType)
	if kind == "image" {
		if err := generation.ValidateImageArtifact(header, mimeType); err != nil {
			return asset.RecoveredArtifact{}, release, err
		}
	}
	name := result.FileName
	ext := asset.ExtensionFromMimeType(mimeType)
	if name == "" {
		name = "generated." + ext
	} else if current := strings.ToLower(filepath.Ext(name)); ext != "bin" && (current == ".bin" || (kind == "video" && current != "" && current != "."+ext)) {
		name = strings.TrimSuffix(name, filepath.Ext(name)) + "." + ext
	}
	artifact := asset.RecoveredArtifact{Kind: kind, FileName: name, MimeType: mimeType, Size: result.Size, Body: file}
	if kind == "image" {
		artifact.Width, artifact.Height = decodeImageDimensions(header)
	}
	if kind == "video" || kind == "audio" {
		if facts, err := mediatools.ProbeLocal(ctx, file.Name()); err == nil {
			artifact.Width, artifact.Height, artifact.DurationMs = facts.Width, facts.Height, facts.DurationMs
		}
	}
	return artifact, release, nil
}
