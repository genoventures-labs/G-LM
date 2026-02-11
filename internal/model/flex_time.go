package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

var timeFormats = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.000Z07:00",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02 15:04:05.999999999Z",
	"2006-01-02 15:04:05.000Z",
	"2006-01-02 15:04:05Z",
}

type FlexTime struct {
	time.Time
}

func NewFlexTime(t time.Time) *FlexTime {
	return &FlexTime{Time: t}
}

func NewFlexTimeValue(t *time.Time) *FlexTime {
	if t == nil {
		return nil
	}
	return NewFlexTime(t.UTC())
}

func (ft *FlexTime) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		ft.Time = time.Time{}
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	s = strings.TrimSpace(s)
	if s == "" {
		ft.Time = time.Time{}
		return nil
	}
	for _, layout := range timeFormats {
		if t, err := time.Parse(layout, s); err == nil {
			ft.Time = t
			return nil
		}
	}
	return fmt.Errorf("unsupported time format: %q", s)
}

func (ft FlexTime) MarshalJSON() ([]byte, error) {
	if ft.Time.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(ft.Time.UTC().Format(time.RFC3339Nano))
}
