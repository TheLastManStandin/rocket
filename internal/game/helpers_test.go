package game

import (
	"encoding/json"
	"strings"
)

func marshal(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

func eventsOfType(events []Event, typ string) []Event {
	var out []Event
	for _, e := range events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func hasEvent(events []Event, typ string) bool { return len(eventsOfType(events, typ)) > 0 }

func hasAnyEvent(events []Event, types ...string) bool {
	for _, typ := range types {
		if hasEvent(events, typ) {
			return true
		}
	}
	return false
}
