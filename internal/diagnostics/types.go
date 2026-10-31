// Пакет diagnostics делает ограниченные проверки и не врёт про результат.
package diagnostics

import "time"

type Level string

const (
	LevelComponents Level = "A"
	LevelConnection Level = "B"
	LevelEfficacy   Level = "C"
)

type Result string

const (
	ResultPass         Result = "pass"
	ResultFail         Result = "fail"
	ResultSkipped      Result = "skipped"
	ResultInsufficient Result = "insufficient"
)

type Record struct {
	Time          time.Time `json:"time"`
	Level         Level     `json:"level"`
	Type          string    `json:"type"`
	Target        string    `json:"target"`
	Result        Result    `json:"result"`
	Detail        string    `json:"detail,omitempty"`
	Reason        string    `json:"reason,omitempty"`
	EngineVersion string    `json:"engineVersion,omitempty"`
	ProfileID     string    `json:"profileId,omitempty"`
	DurationMS    int64     `json:"durationMs"`
}

type Summary struct {
	Pass         int `json:"pass"`
	Fail         int `json:"fail"`
	Skipped      int `json:"skipped"`
	Insufficient int `json:"insufficient"`
}

func Summarize(records []Record) Summary {
	var s Summary
	for _, r := range records {
		switch r.Result {
		case ResultPass:
			s.Pass++
		case ResultFail:
			s.Fail++
		case ResultSkipped:
			s.Skipped++
		default:
			s.Insufficient++
		}
	}
	return s
}

type Report struct {
	GeneratedAt time.Time `json:"generatedAt"`
	AppVersion  string    `json:"appVersion"`
	OS          string    `json:"os"`
	Arch        string    `json:"arch"`
	Summary     Summary   `json:"summary"`
	Records     []Record  `json:"records"`
	Notes       []string  `json:"notes"`
}
