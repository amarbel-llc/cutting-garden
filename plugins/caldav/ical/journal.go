package ical

import (
	"fmt"
	"strconv"
	"strings"
)

// Journal is the structured representation of a VJOURNAL component (RFC 5545
// §3.6.3) — a dated note. It mirrors Event/Task but carries only the
// journal-relevant properties: no DtEnd/Location/Duration (event-only) and no
// VALARM (RFC 5545 permits alarms only in VEVENT/VTODO).
type Journal struct {
	UID          string   `json:"uid"`
	Summary      string   `json:"summary"`
	Description  string   `json:"description,omitempty"`
	Status       string   `json:"status,omitempty"`
	DtStart      string   `json:"dtstart,omitempty"`
	Categories   []string `json:"categories,omitempty"`
	Created      string   `json:"created,omitempty"`
	LastModified string   `json:"last_modified,omitempty"`
	Sequence     int      `json:"sequence,omitempty"`

	// Derived fields
	HasDescription    bool `json:"has_description"`
	DescriptionTokens int  `json:"description_tokens"`

	// Server metadata
	Href string `json:"href,omitempty"`
	ETag string `json:"etag,omitempty"`
}

// JournalMetadata is the lightweight tier-1 view of a journal entry.
type JournalMetadata struct {
	UID               string   `json:"uid"`
	Summary           string   `json:"summary"`
	Status            string   `json:"status,omitempty"`
	DtStart           string   `json:"dtstart,omitempty"`
	Categories        []string `json:"categories,omitempty"`
	HasDescription    bool     `json:"has_description"`
	DescriptionTokens int      `json:"description_tokens"`
}

// ToMetadata converts a full Journal to its lightweight metadata view.
func (j *Journal) ToMetadata() JournalMetadata {
	return JournalMetadata{
		UID:               j.UID,
		Summary:           j.Summary,
		Status:            j.Status,
		DtStart:           j.DtStart,
		Categories:        j.Categories,
		HasDescription:    j.HasDescription,
		DescriptionTokens: j.DescriptionTokens,
	}
}

// ParseVJOURNAL parses a raw iCalendar string and extracts the first VJOURNAL
// as a Journal.
func ParseVJOURNAL(raw string) (*Journal, error) {
	lines := unfoldLines(raw)

	inVJOURNAL := false
	j := &Journal{}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if trimmed == "BEGIN:VJOURNAL" {
			inVJOURNAL = true
			continue
		}
		if trimmed == "END:VJOURNAL" {
			inVJOURNAL = false
			break
		}
		if !inVJOURNAL {
			continue
		}

		name, value := parsePropLine(trimmed)

		switch propName(name) {
		case "UID":
			j.UID = value
		case "SUMMARY":
			j.Summary = value
		case "DESCRIPTION":
			j.Description = value
		case "STATUS":
			j.Status = value
		case "DTSTART":
			j.DtStart = value
		case "CATEGORIES":
			cats := strings.Split(value, ",")
			for i := range cats {
				cats[i] = strings.TrimSpace(cats[i])
			}
			j.Categories = cats
		case "CREATED":
			j.Created = value
		case "LAST-MODIFIED":
			j.LastModified = value
		case "SEQUENCE":
			if n, err := strconv.Atoi(value); err == nil {
				j.Sequence = n
			}
		}
	}

	if j.UID == "" {
		return nil, fmt.Errorf("VJOURNAL missing UID")
	}

	j.HasDescription = j.Description != ""
	j.DescriptionTokens = len(j.Description) / 4

	return j, nil
}

// JournalToIcal serializes a Journal to a full VCALENDAR string.
func JournalToIcal(j *Journal) string {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\n")
	b.WriteString("VERSION:2.0\r\n")
	b.WriteString("PRODID:-//amarbel-llc//cutting-garden//EN\r\n")
	b.WriteString("BEGIN:VJOURNAL\r\n")

	writeIcalProp(&b, "UID", j.UID)
	writeIcalProp(&b, "DTSTAMP", formatNow())
	writeIcalProp(&b, "SUMMARY", j.Summary)

	if j.Description != "" {
		writeIcalProp(&b, "DESCRIPTION", j.Description)
	}
	if j.Status != "" {
		writeIcalProp(&b, "STATUS", j.Status)
	}
	if j.DtStart != "" {
		writeDateProp(&b, "DTSTART", j.DtStart)
	}
	if len(j.Categories) > 0 {
		writeIcalProp(&b, "CATEGORIES", strings.Join(j.Categories, ","))
	}
	if j.Sequence > 0 {
		writeIcalProp(&b, "SEQUENCE", strconv.Itoa(j.Sequence))
	}

	b.WriteString("END:VJOURNAL\r\n")
	b.WriteString("END:VCALENDAR\r\n")
	return b.String()
}
