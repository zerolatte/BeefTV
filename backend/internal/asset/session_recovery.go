package asset

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"infinite-canvas/backend/internal/model"
)

func (s *Service) abandonStaleSessions() {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	if err := s.abandonStaleSessionsLocked(); err != nil {
		log.Printf("upload reservation recovery failed: %v", err)
	}
}

func (s *Service) abandonStaleSessionsLocked() error {
	if s.resumeChunkedUploads {
		if err := s.reconcileSessionDirsLocked(); err != nil {
			return err
		}
		return s.releaseOrphanReservationsLocked()
	}
	first := s.releaseOrphanReservationsLocked()
	if err := s.reconcileSessionDirsLocked(); err != nil && first == nil {
		first = err
	}
	return first
}

func (s *Service) releaseOrphanReservationsLocked() error {
	if s == nil || s.repo == nil {
		return nil
	}
	rows, err := s.repo.ListUploadReservations()
	if err != nil {
		return err
	}
	var first error
	for _, row := range rows {
		row := row
		if s.liveSessionByIdentityLocked(row.UserID, row.Identity) != nil {
			continue
		}
		existing, lookupErr := s.resourceForUploadKey(row.UserID, &row.Identity)
		if lookupErr != nil {
			if first == nil {
				first = lookupErr
			}
			continue
		}
		if settleErr := s.settleOrphanReservation(row, existing); settleErr != nil && first == nil {
			first = settleErr
		}
	}
	return first
}

func (s *Service) settleOrphanReservation(row model.UserUploadReservation, existing *model.Resource) error {
	if row.Unattributed() {
		return nil
	}
	if existing != nil && existing.Status == model.ResourceStatusPending {
		return nil
	}
	if existing != nil && existing.Status == model.ResourceStatusReady {
		return s.commitWitness(row.UserID, row.Identity, row.Size)
	}
	if existing != nil && existing.Status != model.ResourceStatusFailed {
		return s.commitWitness(row.UserID, row.Identity, row.Size)
	}
	return s.releaseWitness(row)
}

func (s *Service) commitWitness(userID, identity string, size int64) error {
	if s == nil || s.repo == nil {
		return nil
	}
	if err := s.repo.ClearUploadReservation(userID, identity); err != nil {
		return err
	}
	s.quotaCommit(userID, size, identity)
	return nil
}

func (s *Service) releaseWitness(row model.UserUploadReservation) error {
	if s == nil || s.repo == nil {
		return nil
	}
	if err := s.repo.ReleaseIdentifiedDailyUpload(row.UserID, row.Day, row.Identity, row.Size); err != nil {
		return err
	}
	s.quotaRelease(row.UserID, row.Day, row.Size, row.Identity)
	return nil
}

func (s *Service) reconcileSessionDirsLocked() error {
	root := s.sessionRoot()
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var first error
	keep := func(err error) {
		if first == nil {
			first = err
		}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if s.sessions[entry.Name()] != nil {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		payload, readErr := os.ReadFile(filepath.Join(dir, chunkSessionMetaName))
		if readErr != nil {
			continue
		}
		var meta chunkSessionMeta
		if json.Unmarshal(payload, &meta) != nil || strings.TrimSpace(meta.UserID) == "" || strings.TrimSpace(meta.Identity) == "" {
			continue
		}
		existing, lookupErr := s.resourceForUploadKey(meta.UserID, &meta.Identity)
		if lookupErr != nil {
			keep(lookupErr)
			continue
		}
		if existing != nil {
			_ = os.RemoveAll(dir)
			continue
		}
		if s.resumeChunkedUploads && meta.Reserved && time.Since(meta.CreatedAt) < chunkUploadTTL &&
			meta.ID == entry.Name() && meta.Size > 0 && meta.Size <= (1<<62) &&
			meta.ChunkCount == int((meta.Size+ChunkUploadSize-1)/ChunkUploadSize) {
			held, heldErr := s.lookupReservation(meta.UserID, meta.Identity)
			if heldErr != nil {
				keep(heldErr)
				continue
			}
			if held != nil && held.Size >= meta.Size {
				s.sessions[meta.ID] = &chunkedUploadSession{
					ID: meta.ID, UserID: meta.UserID, FileName: meta.FileName, Kind: meta.Kind,
					Size: meta.Size, Width: meta.Width, Height: meta.Height, DurationMs: meta.DurationMs,
					Identity: meta.Identity, ContentDigest: meta.ContentDigest, Day: meta.Day, Reserved: true, ChunkCount: meta.ChunkCount,
					Dir: dir, CreatedAt: meta.CreatedAt,
				}
				continue
			}
		}
		if !meta.Reserved {
			held, heldErr := s.reservationHeld(meta.UserID, meta.Identity)
			if heldErr != nil {
				keep(heldErr)
				continue
			}
			if !held {
				_ = os.RemoveAll(dir)
				continue
			}
		}
		s.quotaRelease(meta.UserID, meta.Day, meta.Size, meta.Identity)
		_ = os.RemoveAll(dir)
	}
	return first
}

func (s *Service) reservationHeld(userID, identity string) (bool, error) {
	row, err := s.lookupReservation(userID, identity)
	if err != nil {
		return false, err
	}
	return row != nil, nil
}

func (s *Service) lookupReservation(userID, identity string) (*model.UserUploadReservation, error) {
	if s == nil || s.repo == nil {
		return nil, nil
	}
	return s.repo.UploadReservation(userID, identity)
}
