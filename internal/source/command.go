package source

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/identity"
	"github.com/abandon1a2b/ohyeah/internal/model"
	"github.com/abandon1a2b/ohyeah/pkg/collector"
)

const commandParserVersion = 1

type Command struct {
	executable string
	args       []string
}

func NewCommand(source model.Source) (Command, error) {
	executable, _ := source.Options["command"].(string)
	if strings.TrimSpace(executable) == "" {
		return Command{}, errors.New("command collector requires options.command")
	}
	args, err := stringSlice(source.Options["args"])
	if err != nil {
		return Command{}, err
	}
	return Command{executable: executable, args: args}, nil
}

func (c Command) Scan(ctx context.Context, source model.Source, maxFileSize int64) (model.ScanResult, error) {
	executable := c.executable
	if !filepath.IsAbs(executable) && strings.ContainsRune(executable, filepath.Separator) {
		executable = filepath.Join(source.Path, executable)
	}
	cmd := exec.CommandContext(ctx, executable, c.args...)
	configureProcess(cmd)
	cmd.Dir = source.Path
	cmd.Env = append(os.Environ(), "OHYEAH_COLLECTOR_PROTOCOL=1")
	request := collector.Request{
		ProtocolVersion: collector.ProtocolVersion,
		Project:         collector.Project{ID: source.ProjectID, Workspace: projectWorkspace(source)},
		Type:            collector.DataType{ID: source.TypeID, Revision: source.TypeRevision},
		Mount:           collector.Mount{ID: mountName(source), Root: source.Path, Revision: source.MountRevision, Options: publicOptions(source.Options)},
		Cursor:          source.Cursor, MaxFileSize: maxFileSize,
	}
	input, err := json.Marshal(request)
	if err != nil {
		return model.ScanResult{}, err
	}
	cmd.Stdin = bytes.NewReader(append(input, '\n'))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return model.ScanResult{}, err
	}
	var stderr limitedBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return model.ScanResult{}, fmt.Errorf("start collector: %w", err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = stdout.Close()
			terminateProcess(cmd)
		}
	}()

	var collected []collector.Document
	var deletedExternalIDs []string
	var cursor json.RawMessage
	completionMode := ""
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 32<<20)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		var envelope collector.Envelope
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			return model.ScanResult{}, fmt.Errorf("collector stdout line %d is not valid protocol JSON: %w", lineNumber, err)
		}
		switch envelope.Type {
		case collector.EnvelopeDocument:
			if completionMode != "" {
				return model.ScanResult{}, fmt.Errorf("collector emitted data after complete on line %d", lineNumber)
			}
			if envelope.Document == nil {
				return model.ScanResult{}, fmt.Errorf("collector stdout line %d has no document", lineNumber)
			}
			collected = append(collected, *envelope.Document)
		case collector.EnvelopeDelete:
			if completionMode != "" {
				return model.ScanResult{}, fmt.Errorf("collector emitted data after complete on line %d", lineNumber)
			}
			if envelope.ExternalID == "" {
				return model.ScanResult{}, fmt.Errorf("collector delete on line %d has no externalId", lineNumber)
			}
			deletedExternalIDs = append(deletedExternalIDs, envelope.ExternalID)
		case collector.EnvelopeDiagnostic:
			slog.Log(ctx, diagnosticLevel(envelope.Level), envelope.Message, "source", source.ID)
		case collector.EnvelopeComplete:
			if completionMode != "" {
				return model.ScanResult{}, errors.New("collector emitted more than one complete envelope")
			}
			if envelope.Mode != "snapshot" && envelope.Mode != "delta" {
				return model.ScanResult{}, fmt.Errorf("collector complete envelope requires mode snapshot or delta, got %q", envelope.Mode)
			}
			completionMode = envelope.Mode
			cursor = append(json.RawMessage(nil), envelope.Cursor...)
		default:
			return model.ScanResult{}, fmt.Errorf("collector stdout line %d has unknown envelope type %q", lineNumber, envelope.Type)
		}
	}
	if err := scanner.Err(); err != nil {
		return model.ScanResult{}, err
	}
	if err := cmd.Wait(); err != nil {
		waited = true
		return model.ScanResult{}, fmt.Errorf("collector failed: %w: %s", err, stderr.String())
	}
	waited = true
	if completionMode == "" {
		return model.ScanResult{}, errors.New("collector exited without a complete envelope; no changes were applied")
	}
	documents, err := materializeCollected(source, collected)
	if err != nil {
		return model.ScanResult{}, err
	}
	return model.ScanResult{
		Documents: documents, DeletedExternalIDs: uniqueStrings(deletedExternalIDs), Cursor: cursor, PruneMissing: completionMode == "snapshot",
	}, nil
}

func mountName(source model.Source) string {
	if source.MountName != "" {
		return source.MountName
	}
	return source.ID
}

func projectWorkspace(source model.Source) string {
	if source.ProjectWorkspace != "" {
		return source.ProjectWorkspace
	}
	return source.WorkspaceID
}

func materializeCollected(source model.Source, collected []collector.Document) ([]model.Document, error) {
	documents := make([]model.Document, 0, len(collected))
	unitIDs := make(map[string]string)
	seenDocuments := make(map[string]struct{})
	for _, input := range collected {
		if input.ExternalID == "" {
			return nil, errors.New("collector document externalId is required")
		}
		if _, duplicate := seenDocuments[input.ExternalID]; duplicate {
			return nil, fmt.Errorf("collector emitted duplicate externalId %q", input.ExternalID)
		}
		seenDocuments[input.ExternalID] = struct{}{}
		for _, unit := range input.Units {
			if unit.Key == "" {
				return nil, fmt.Errorf("collector document %q has a unit without key", input.ExternalID)
			}
			ref := collectedRef(input.ExternalID, unit.Key)
			if _, duplicate := unitIDs[ref]; duplicate {
				return nil, fmt.Errorf("collector emitted duplicate unit %q", ref)
			}
			unitIDs[ref] = identity.Hash("collector-unit", source.ID, input.ExternalID, unit.Key)
		}
	}

	for _, input := range collected {
		modifiedAt := input.ModifiedAt
		if modifiedAt.IsZero() {
			modifiedAt = time.Now().UTC()
		}
		objectID := identity.Hash("object", source.ID, input.ExternalID)
		sourceType := input.SourceType
		if sourceType == "" {
			sourceType = "external"
		}
		document := model.Document{Object: model.SourceObject{
			ID: objectID, SourceID: source.ID, ExternalID: input.ExternalID,
			ModifiedAt: modifiedAt.UTC(), ParserVersion: commandParserVersion, Reference: input.Reference,
		}}
		var hashes []string
		for _, inputUnit := range input.Units {
			occurredAt := inputUnit.OccurredAt
			if occurredAt.IsZero() {
				occurredAt = modifiedAt
			}
			status := inputUnit.Status
			if status == "" {
				status = model.StatusObserved
			}
			kind := inputUnit.Kind
			if kind == "" {
				kind = model.KindDocument
			}
			contentHash := identity.Hash(inputUnit.Content)
			hashes = append(hashes, contentHash)
			document.Units = append(document.Units, model.MemoryUnit{
				ID: unitIDs[collectedRef(input.ExternalID, inputUnit.Key)], ObjectID: objectID,
				ProjectID: source.ProjectID, TypeID: source.TypeID, MountID: source.ID,
				WorkspaceID: source.WorkspaceID, SourceID: source.ID, SourceType: sourceType,
				Kind: kind, Title: inputUnit.Title, Content: inputUnit.Content,
				ThreadID: inputUnit.ThreadID, TurnID: inputUnit.TurnID, Path: inputUnit.Path, Heading: inputUnit.Heading,
				OccurredAt: occurredAt.UTC(), ContentHash: contentHash, Status: status,
				RequirementIDs: inputUnit.RequirementIDs, Repos: inputUnit.Repos, Entities: inputUnit.Entities, Metadata: inputUnit.Metadata,
			})
		}
		document.Object.ContentHash = identity.Hash(strings.Join(hashes, ""))
		for _, relation := range input.Relations {
			fromExternalID := relation.FromExternalID
			if fromExternalID == "" {
				fromExternalID = input.ExternalID
			}
			toExternalID := relation.ToExternalID
			if toExternalID == "" {
				toExternalID = input.ExternalID
			}
			fromID, fromOK := unitIDs[collectedRef(fromExternalID, relation.FromKey)]
			toID, toOK := unitIDs[collectedRef(toExternalID, relation.ToKey)]
			if !fromOK || !toOK {
				return nil, fmt.Errorf("collector relation references an unknown unit: %s#%s -> %s#%s", fromExternalID, relation.FromKey, toExternalID, relation.ToKey)
			}
			confidence := relation.Confidence
			if confidence == 0 {
				confidence = 1
			}
			document.Relations = append(document.Relations, model.Relation{
				ID:     identity.Hash("collector-relation", source.ID, fromID, toID, relation.Type),
				FromID: fromID, ToID: toID, Type: relation.Type, Confidence: confidence, CreatedAt: modifiedAt.UTC(),
			})
		}
		documents = append(documents, document)
	}
	return documents, nil
}

func publicOptions(options map[string]any) map[string]any {
	result := make(map[string]any, len(options))
	for key, value := range options {
		if key != "command" && key != "args" && !strings.HasPrefix(key, "__ohyeah_") {
			result[key] = value
		}
	}
	return result
}

func stringSlice(value any) ([]string, error) {
	switch values := value.(type) {
	case nil:
		return nil, nil
	case []string:
		return values, nil
	case []any:
		result := make([]string, 0, len(values))
		for _, value := range values {
			stringValue, ok := value.(string)
			if !ok {
				return nil, errors.New("command collector options.args must contain only strings")
			}
			result = append(result, stringValue)
		}
		return result, nil
	default:
		return nil, errors.New("command collector options.args must be an array")
	}
}

func collectedRef(externalID, key string) string { return externalID + "\x00" + key }

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func diagnosticLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

type limitedBuffer struct {
	buffer bytes.Buffer
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	const limit = 64 << 10
	original := len(value)
	if b.buffer.Len() < limit {
		remaining := limit - b.buffer.Len()
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = b.buffer.Write(value)
	}
	return original, nil
}

func (b *limitedBuffer) String() string { return b.buffer.String() }

var _ io.Writer = (*limitedBuffer)(nil)
