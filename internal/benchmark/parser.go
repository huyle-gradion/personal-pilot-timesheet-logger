package benchmark

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
)

// ErrEmptyTranscript is returned when the transcript contains no usable token entries.
var ErrEmptyTranscript = errors.New("transcript contains no token records")

// rawTokenRecord captures potential token fields across multiple JSONL transcript formats.
type rawTokenRecord struct {
	InputTokens     *int64 `json:"input_tokens"`
	OutputTokens    *int64 `json:"output_tokens"`
	CacheReadTokens *int64 `json:"cache_read_tokens"`
	CacheTokens     *int64 `json:"cache_tokens"`
	TotalTokens     *int64 `json:"total_tokens"`

	Usage *struct {
		InputTokens            *int64 `json:"input_tokens"`
		OutputTokens           *int64 `json:"output_tokens"`
		CacheReadInputTokens   *int64 `json:"cache_read_input_tokens"`
		CacheReadTokens        *int64 `json:"cache_read_tokens"`
		CacheCreationInputToks *int64 `json:"cache_creation_input_tokens"`
		TotalTokens            *int64 `json:"total_tokens"`
	} `json:"usage"`

	Message *struct {
		Usage *struct {
			InputTokens          *int64 `json:"input_tokens"`
			OutputTokens         *int64 `json:"output_tokens"`
			CacheReadInputTokens *int64 `json:"cache_read_input_tokens"`
			TotalTokens          *int64 `json:"total_tokens"`
		} `json:"usage"`
	} `json:"message"`

	Response *struct {
		Usage *struct {
			InputTokens          *int64 `json:"input_tokens"`
			OutputTokens         *int64 `json:"output_tokens"`
			CacheReadInputTokens *int64 `json:"cache_read_input_tokens"`
			TotalTokens          *int64 `json:"total_tokens"`
		} `json:"usage"`
	} `json:"response"`
}

// ParseTranscript streams a JSONL transcript from r and aggregates all LLM token counters.
func ParseTranscript(ctx context.Context, r io.Reader) (TokenUsage, error) {
	Assert(ctx != nil, AssertBenchmarkContextNil)
	Assert(r != nil, AssertBenchmarkNilReader)

	scanner := bufio.NewScanner(r)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, MaxTranscriptLineSizeBytes)

	var usage TokenUsage
	var recordsFound int

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return TokenUsage{}, err
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}

		var rec rawTokenRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			// Skip unparseable lines gracefully (e.g. non-JSON debug logs)
			continue
		}

		inp, out, cache := extractTokens(rec)
		if inp > 0 || out > 0 || cache > 0 {
			usage.InputTokens += inp
			usage.OutputTokens += out
			usage.CacheTokens += cache
			recordsFound++
		}
	}

	if err := scanner.Err(); err != nil {
		return TokenUsage{}, fmt.Errorf("reading transcript: %w", err)
	}

	usage.TotalTokens = usage.InputTokens + usage.OutputTokens + usage.CacheTokens
	return usage, nil
}

// ParseTranscriptFile safely opens and parses a transcript file at path.
func ParseTranscriptFile(ctx context.Context, path string) (TokenUsage, error) {
	Assert(ctx != nil, AssertBenchmarkContextNil)

	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return TokenUsage{}, fmt.Errorf("opening transcript file %s: %w", path, err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return TokenUsage{}, fmt.Errorf("stat transcript file: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return TokenUsage{}, fmt.Errorf("transcript %s is not a regular file", path)
	}

	return ParseTranscript(ctx, f)
}

func extractTokens(rec rawTokenRecord) (input, output, cache int64) {
	// 1. Top level
	if rec.InputTokens != nil {
		input = *rec.InputTokens
	}
	if rec.OutputTokens != nil {
		output = *rec.OutputTokens
	}
	if rec.CacheReadTokens != nil {
		cache = *rec.CacheReadTokens
	} else if rec.CacheTokens != nil {
		cache = *rec.CacheTokens
	}

	// 2. Nested under usage
	if rec.Usage != nil {
		if rec.Usage.InputTokens != nil {
			input += *rec.Usage.InputTokens
		}
		if rec.Usage.OutputTokens != nil {
			output += *rec.Usage.OutputTokens
		}
		if rec.Usage.CacheReadInputTokens != nil {
			cache += *rec.Usage.CacheReadInputTokens
		} else if rec.Usage.CacheReadTokens != nil {
			cache += *rec.Usage.CacheReadTokens
		}
	}

	// 3. Nested under message.usage
	if rec.Message != nil && rec.Message.Usage != nil {
		if rec.Message.Usage.InputTokens != nil {
			input += *rec.Message.Usage.InputTokens
		}
		if rec.Message.Usage.OutputTokens != nil {
			output += *rec.Message.Usage.OutputTokens
		}
		if rec.Message.Usage.CacheReadInputTokens != nil {
			cache += *rec.Message.Usage.CacheReadInputTokens
		}
	}

	// 4. Nested under response.usage
	if rec.Response != nil && rec.Response.Usage != nil {
		if rec.Response.Usage.InputTokens != nil {
			input += *rec.Response.Usage.InputTokens
		}
		if rec.Response.Usage.OutputTokens != nil {
			output += *rec.Response.Usage.OutputTokens
		}
		if rec.Response.Usage.CacheReadInputTokens != nil {
			cache += *rec.Response.Usage.CacheReadInputTokens
		}
	}

	return input, output, cache
}
