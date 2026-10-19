// Package metrics считает агрегаты без хранения исходных строк или идентификаторов событий.
package metrics

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"unicode/utf8"
)

const (
	MaxLineBytes          = 8192
	HardMaxBytes    int64 = 2 << 30
	HardMaxRows     int64 = 100_000_000
	HardMaxServices       = 1024
)

var bucketBounds = [...]int64{50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000, 60000}
var servicePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
var integerPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

type Options struct {
	MaxServices int
	SlowMS      int64
	MaxBytes    int64
	MaxRows     int64
}

func DefaultOptions() Options {
	return Options{MaxServices: 64, SlowMS: 500, MaxBytes: HardMaxBytes, MaxRows: HardMaxRows}
}

type Service struct {
	Name         string    `json:"service"`
	Requests     int64     `json:"requests"`
	ServerErrors int64     `json:"server_errors"`
	ClientErrors int64     `json:"client_errors"`
	SlowRequests int64     `json:"slow_requests"`
	AverageMS    float64   `json:"avg_duration_ms"`
	ErrorRate    float64   `json:"server_error_rate_pct"`
	P95UpperMS   int64     `json:"p95_upper_ms"`
	Buckets      [10]int64 `json:"bucket_counts"`
	durationSum  int64
}

type Summary struct {
	InputRows     int64            `json:"input_rows"`
	AcceptedRows  int64            `json:"accepted_rows"`
	RejectedRows  int64            `json:"rejected_rows"`
	InputBytes    int64            `json:"input_bytes"`
	SourceSHA256  string           `json:"source_sha256"`
	SlowMS        int64            `json:"slow_ms"`
	BucketUpperMS [10]int64        `json:"bucket_upper_ms"`
	Reasons       map[string]int64 `json:"rejection_reasons"`
	Services      []Service        `json:"services"`
}

type event struct {
	service          string
	status, duration int64
}

// parse не допускает повторных ключей, null, дробей и нескольких JSON-значений в строке.
func parse(line []byte) (event, string) {
	if !utf8.Valid(line) {
		return event{}, "json"
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return event{}, "json"
	}
	values := make(map[string]json.RawMessage, 3)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return event{}, "json"
		}
		name, ok := key.(string)
		if !ok {
			return event{}, "json"
		}
		if _, exists := values[name]; exists {
			return event{}, "schema"
		}
		if name != "service" && name != "status" && name != "duration_ms" {
			return event{}, "schema"
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return event{}, "json"
		}
		values[name] = value
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return event{}, "json"
	}
	if _, err = decoder.Token(); err != io.EOF {
		return event{}, "json"
	}
	if len(values) != 3 {
		return event{}, "schema"
	}
	var e event
	if err := json.Unmarshal(values["service"], &e.service); err != nil || !servicePattern.MatchString(e.service) {
		return event{}, "service"
	}
	for _, field := range []struct {
		name     string
		target   *int64
		min, max int64
	}{
		{"status", &e.status, 100, 599}, {"duration_ms", &e.duration, 0, 60000},
	} {
		value := bytes.TrimSpace(values[field.name])
		if !integerPattern.Match(value) {
			return event{}, field.name
		}
		number, err := strconv.ParseInt(string(value), 10, 64)
		if err != nil || number < field.min || number > field.max {
			return event{}, field.name
		}
		*field.target = number
	}
	return e, ""
}

type countingReader struct {
	reader io.Reader
	bytes  int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.bytes += int64(n)
	return n, err
}

func Aggregate(reader io.Reader, options Options) (Summary, error) {
	if options.MaxServices < 1 || options.MaxServices > HardMaxServices || options.SlowMS < 0 || options.SlowMS > 60000 ||
		options.MaxBytes < 1 || options.MaxBytes > HardMaxBytes || options.MaxRows < 1 || options.MaxRows > HardMaxRows {
		return Summary{}, errors.New("Параметры обработки выходят за допустимые пределы.")
	}
	result := Summary{SlowMS: options.SlowMS, BucketUpperMS: bucketBounds, Reasons: make(map[string]int64), Services: []Service{}}
	groups := make(map[string]*Service)
	counter := &countingReader{reader: io.LimitReader(reader, options.MaxBytes+1)}
	hash := sha256.New()
	scanner := bufio.NewScanner(io.TeeReader(counter, hash))
	scanner.Buffer(make([]byte, 4096), MaxLineBytes+2)
	for scanner.Scan() {
		if counter.bytes > options.MaxBytes {
			return Summary{}, errors.New("Превышен предел размера входа.")
		}
		if result.InputRows >= options.MaxRows {
			return Summary{}, errors.New("Превышен предел числа строк.")
		}
		result.InputRows++
		line := scanner.Bytes()
		if len(line) > MaxLineBytes {
			return Summary{}, errors.New("Строка превышает 8192 байта.")
		}
		e, reason := parse(line)
		if reason != "" {
			result.RejectedRows++
			result.Reasons[reason]++
			continue
		}
		group, exists := groups[e.service]
		if !exists {
			if len(groups) >= options.MaxServices {
				return Summary{}, errors.New("Превышен предел числа сервисов; результат не сформирован.")
			}
			group = &Service{Name: e.service}
			groups[e.service] = group
		}
		result.AcceptedRows++
		group.Requests++
		group.durationSum += e.duration
		if e.status >= 500 {
			group.ServerErrors++
		} else if e.status >= 400 {
			group.ClientErrors++
		}
		if e.duration >= options.SlowMS {
			group.SlowRequests++
		}
		for i, upper := range bucketBounds {
			if e.duration <= upper {
				group.Buckets[i]++
				break
			}
		}
	}
	if scanner.Err() != nil {
		return Summary{}, errors.New("Не удалось дочитать вход: ошибка чтения или слишком длинная строка.")
	}
	if counter.bytes > options.MaxBytes {
		return Summary{}, errors.New("Превышен предел размера входа.")
	}
	result.InputBytes = counter.bytes
	result.SourceSHA256 = hex.EncodeToString(hash.Sum(nil))
	for _, group := range groups {
		group.AverageMS = math.Round(float64(group.durationSum)*100/float64(group.Requests)) / 100
		group.ErrorRate = math.Round(float64(group.ServerErrors)*10000/float64(group.Requests)) / 100
		rank := (95*group.Requests + 99) / 100
		var cumulative int64
		for i, count := range group.Buckets {
			cumulative += count
			if cumulative >= rank {
				group.P95UpperMS = bucketBounds[i]
				break
			}
		}
		result.Services = append(result.Services, *group)
	}
	sort.Slice(result.Services, func(i, j int) bool { return result.Services[i].Name < result.Services[j].Name })
	return result, nil
}

func (s Summary) Russian() string {
	text := fmt.Sprintf("Входных строк: %d\nПринято: %d\nОтклонено: %d\nСервисов: %d\n", s.InputRows, s.AcceptedRows, s.RejectedRows, len(s.Services))
	for _, group := range s.Services {
		text += fmt.Sprintf("%s: запросов %d; 5xx %d; 4xx %d; среднее %.2f мс; p95 ≤ %d мс\n",
			group.Name, group.Requests, group.ServerErrors, group.ClientErrors, group.AverageMS, group.P95UpperMS)
	}
	return text
}
