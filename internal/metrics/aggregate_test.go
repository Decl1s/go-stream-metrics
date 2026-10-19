package metrics

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

func row(service string, status, duration int) string {
	return fmt.Sprintf("{\"service\":%q,\"status\":%d,\"duration_ms\":%d}\n", service, status, duration)
}

func aggregate(t *testing.T, input string) Summary {
	t.Helper()
	s, err := Aggregate(strings.NewReader(input), DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestManualMetricsAndApproximateP95(t *testing.T) {
	s := aggregate(t, row("api", 200, 100)+row("api", 503, 200)+row("api", 404, 300))
	g := s.Services[0]
	if s.AcceptedRows != 3 || g.AverageMS != 200 || g.ServerErrors != 1 || g.ClientErrors != 1 || g.ErrorRate != 33.33 || g.P95UpperMS != 500 {
		t.Fatalf("Неверные метрики: %+v", s)
	}
}

func TestAllBucketBoundaries(t *testing.T) {
	var input strings.Builder
	for _, value := range []int{0, 50, 51, 100, 101, 250, 251, 500, 501, 1000, 1001, 2500, 2501, 5000, 5001, 10000, 10001, 30000, 30001, 60000} {
		input.WriteString(row("api", 200, value))
	}
	s := aggregate(t, input.String())
	for _, count := range s.Services[0].Buckets {
		if count != 2 {
			t.Fatalf("Граница интервала неверна: %+v", s.Services[0].Buckets)
		}
	}
}

func TestSlowThresholdIsInclusive(t *testing.T) {
	s := aggregate(t, row("api", 200, 499)+row("api", 200, 500)+row("api", 200, 501))
	if s.Services[0].SlowRequests != 2 {
		t.Fatal(s.Services[0])
	}
}

func TestDuplicateEventsAreCountedAgain(t *testing.T) {
	s := aggregate(t, strings.Repeat(row("api", 200, 100), 2))
	if s.AcceptedRows != 2 || s.Services[0].Requests != 2 {
		t.Fatal(s)
	}
}

func TestMalformedRowsHaveOnlyFiniteReasonCodes(t *testing.T) {
	cases := map[string]string{
		`{`: "json", `[]`: "json", `{"service":"api","status":200,"duration_ms":1} {}`: "json",
		`{"service":"api","service":"api","status":200,"duration_ms":1}`: "schema",
		`{"service":"api","status":200,"duration_ms":1,"other":0}`:       "schema",
		`{"service":"api","status":200}`:                                 "schema", `{"service":"bad space","status":200,"duration_ms":1}`: "service",
		`{"service":null,"status":200,"duration_ms":1}`: "service", `{"service":123,"status":200,"duration_ms":1}`: "service",
		`{"service":"api","status":"200","duration_ms":1}`: "status", `{"service":"api","status":null,"duration_ms":1}`: "status",
		`{"service":"api","status":true,"duration_ms":1}`: "status", `{"service":"api","status":200.0,"duration_ms":1}`: "status",
		`{"service":"api","status":99,"duration_ms":1}`: "status", `{"service":"api","status":600,"duration_ms":1}`: "status",
		`{"service":"api","status":200,"duration_ms":-1}`: "duration_ms", `{"service":"api","status":200,"duration_ms":60001}`: "duration_ms",
		`{"service":"api","status":200,"duration_ms":1e2}`: "duration_ms", `{"service":"api","status":200,"duration_ms":null}`: "duration_ms",
		string([]byte{0xff}): "json", "": "json",
	}
	for input, reason := range cases {
		t.Run(reason+fmt.Sprint(len(input)), func(t *testing.T) {
			s := aggregate(t, input+"\n")
			if s.RejectedRows != 1 || s.Reasons[reason] != 1 || s.AcceptedRows != 0 {
				t.Fatalf("Причина %s: %+v", reason, s)
			}
		})
	}
}

func TestServiceNamesAreBoundedAndSorted(t *testing.T) {
	s := aggregate(t, row("worker", 200, 1)+row("api", 200, 1)+row("gateway", 200, 1))
	if s.Services[0].Name != "api" || s.Services[2].Name != "worker" {
		t.Fatal(s.Services)
	}
	s = aggregate(t, row(strings.Repeat("a", 33), 200, 1))
	if s.Reasons["service"] != 1 {
		t.Fatal(s)
	}
}

func TestTooManyServicesAbortsWithoutPartialSummary(t *testing.T) {
	options := DefaultOptions()
	options.MaxServices = 1
	s, err := Aggregate(strings.NewReader(row("api", 200, 1)+row("worker", 200, 2)), options)
	if err == nil || !reflect.DeepEqual(s, Summary{}) {
		t.Fatalf("Ожидался полный отказ: %+v %v", s, err)
	}
}

func TestLineSizeLimit(t *testing.T) {
	base := strings.TrimSuffix(row("api", 200, 1), "\n")
	input := base + strings.Repeat(" ", MaxLineBytes-len(base))
	if aggregate(t, input+"\r\n").AcceptedRows != 1 {
		t.Fatal("Граничная строка не принята")
	}
	if _, err := Aggregate(strings.NewReader(input+" "), DefaultOptions()); err == nil {
		t.Fatal("Длинная строка принята")
	}
	if _, err := Aggregate(strings.NewReader(strings.Repeat("x", MaxLineBytes*2)), DefaultOptions()); err == nil {
		t.Fatal("Ошибка Scanner потеряна")
	}
}

func TestRowAndByteLimits(t *testing.T) {
	input := row("api", 200, 1)
	options := DefaultOptions()
	options.MaxRows = 1
	if _, err := Aggregate(strings.NewReader(input+input), options); err == nil {
		t.Fatal("Предел строк не сработал")
	}
	options = DefaultOptions()
	options.MaxBytes = int64(len(input)) - 1
	if _, err := Aggregate(strings.NewReader(input), options); err == nil {
		t.Fatal("Предел байтов не сработал")
	}
	options.MaxBytes = int64(len(input))
	if _, err := Aggregate(strings.NewReader(input), options); err != nil {
		t.Fatal(err)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("исходное содержимое не должно выводиться")
}

func TestReaderFailureDoesNotReturnRawErrorOrPartialResult(t *testing.T) {
	s, err := Aggregate(io.MultiReader(strings.NewReader(row("api", 200, 1)), errorReader{}), DefaultOptions())
	if err == nil || strings.Contains(err.Error(), "исходное содержимое") || !reflect.DeepEqual(s, Summary{}) {
		t.Fatalf("Неверная обработка сбоя: %v", err)
	}
}

func TestCRLFAndMissingLastNewlineHashOriginalBytes(t *testing.T) {
	input := strings.ReplaceAll(row("api", 200, 1), "\n", "\r\n") + strings.TrimSuffix(row("api", 200, 2), "\n")
	s := aggregate(t, input)
	hash := sha256.Sum256([]byte(input))
	if s.InputBytes != int64(len(input)) || s.SourceSHA256 != hex.EncodeToString(hash[:]) || s.AcceptedRows != 2 {
		t.Fatal(s)
	}
}

func TestEmptyInputHasNoFakeGroupsOrNaN(t *testing.T) {
	s := aggregate(t, "")
	if s.InputRows != 0 || len(s.Services) != 0 {
		t.Fatal(s)
	}
	if _, err := json.Marshal(s); err != nil {
		t.Fatal(err)
	}
}

func TestRepeatedRunProducesIdenticalSummary(t *testing.T) {
	input := row("api", 503, 300) + "broken\n"
	if !reflect.DeepEqual(aggregate(t, input), aggregate(t, input)) {
		t.Fatal("Повтор изменил результат")
	}
}

func TestOptionValidation(t *testing.T) {
	for _, change := range []func(*Options){func(o *Options) { o.MaxServices = 0 }, func(o *Options) { o.MaxServices = 1025 },
		func(o *Options) { o.SlowMS = -1 }, func(o *Options) { o.SlowMS = 60001 }, func(o *Options) { o.MaxBytes = 0 }, func(o *Options) { o.MaxRows = 0 }} {
		o := DefaultOptions()
		change(&o)
		if _, err := Aggregate(strings.NewReader(""), o); err == nil {
			t.Fatal(o)
		}
	}
}

type repeatingReader struct {
	line              []byte
	remaining, offset int
}

func (r *repeatingReader) Read(p []byte) (int, error) {
	if len(p) > MaxLineBytes+2 {
		return 0, errors.New("обнаружено чтение большим блоком")
	}
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) && r.remaining > 0 {
		copied := copy(p[n:], r.line[r.offset:])
		n += copied
		r.offset += copied
		if r.offset == len(r.line) {
			r.offset = 0
			r.remaining--
		}
	}
	return n, nil
}

func TestLargeReaderUsesBoundedReadChunks(t *testing.T) {
	r := &repeatingReader{line: []byte(row("api", 200, 100)), remaining: 100000}
	s, err := Aggregate(r, DefaultOptions())
	if err != nil || s.AcceptedRows != 100000 || len(s.Services) != 1 {
		t.Fatalf("%+v %v", s, err)
	}
}
