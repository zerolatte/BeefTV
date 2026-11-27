package playback

import (
	"context"
	"infinite-canvas/backend/internal/model"
)

// 远端原件在工作线程内本地化，下载失败保留可重试状态，播放副本仍只存本地。
func (s *Service) launchRemote(resource *model.Resource, force bool) {
	if s.store == nil || s.runner == nil || s.materialize == nil {
		return
	}
	claimed, err := s.store.ClaimPlaybackTranscode(resource.ID)
	if err != nil || !claimed {
		return
	}
	resource.PlaybackStatus = model.PlaybackStatusProcessing
	id, owner := resource.ID, resource.UserID
	if !s.runner.Go(func(ctx context.Context) {
		path, release, err := s.materialize(ctx, owner, id)
		if err != nil {
			s.persistStoppedOrFailed(ctx, id, "", err.Error())
			return
		}
		defer release()
		codec := ProbeCodec(path)
		if !force && codec != CodecH265 && codec != CodecMPEG4 {
			releaseClaim(s.store, id)
			markNone(s.store, id, "")
			return
		}
		s.runTranscode(ctx, id, path)
	}) {
		releaseClaim(s.store, id)
	}
}
