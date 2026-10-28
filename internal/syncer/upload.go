package syncer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"

	"github.com/abandon1a2b/ohyeah/internal/model"
	"github.com/abandon1a2b/ohyeah/internal/source"
	"github.com/abandon1a2b/ohyeah/pkg/collector"
)

var batchIDPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,128}$`)
var ErrInvalidUpload = errors.New("invalid upload")

func (s *Service) Upload(ctx context.Context, registered model.Source, subject, issuer, credential string, batch collector.UploadBatch) (collector.UploadReceipt, error) {
	if batch.ProtocolVersion != collector.ProtocolVersion || batch.SourceID != registered.ID || !batchIDPattern.MatchString(batch.BatchID) || batch.ExpectedVersion < 0 || batch.ScopeRevision == "" {
		return collector.UploadReceipt{}, fmt.Errorf("%w: protocol, source, batch ID, version, or scope", ErrInvalidUpload)
	}
	result, err := source.ParseEnvelopes(ctx, registered, batch.Envelopes)
	if err != nil {
		return collector.UploadReceipt{}, fmt.Errorf("%w: %v", ErrInvalidUpload, err)
	}
	data, err := json.Marshal(batch)
	if err != nil {
		return collector.UploadReceipt{}, err
	}
	sum := sha256.Sum256(data)
	s.commitMu.RLock()
	receipt, err := s.store.CommitUpload(ctx, subject, issuer, credential, batch, hex.EncodeToString(sum[:]), result)
	s.commitMu.RUnlock()
	if err != nil {
		return receipt, err
	}
	// SQLite and outbox are authoritative. An indexing outage must not lose a committed upload.
	if err := s.Dispatch(ctx); err != nil {
		receipt.IndexPending = true
		slog.Warn("upload committed; index delivery pending", "source", registered.ID, "batch", batch.BatchID, "error", err)
	}
	return receipt, nil
}
