// Package report renders longpole's terminal output. Every function here is
// pure: data in, string out, so output shapes can be golden-tested.
package report

import "fmt"

// Dur formats a nanosecond duration for human reading. Builds are measured in
// hundredths of a second; more precision is noise and less hides real cost.
func Dur(ns int64) string {
	if ns == 0 {
		return "0s"
	}
	secs := float64(ns) / 1e9
	if secs >= 60 {
		m := int(secs) / 60
		s := secs - float64(m*60)
		return fmt.Sprintf("%dm%05.2fs", m, s)
	}
	return fmt.Sprintf("%.2fs", secs)
}

// Pct formats part/whole as a rounded integer percentage. A zero whole yields
// "0%" rather than dividing by zero.
func Pct(part, whole int64) string {
	if whole == 0 {
		return "0%"
	}
	return fmt.Sprintf("%.0f%%", float64(part)/float64(whole)*100)
}
