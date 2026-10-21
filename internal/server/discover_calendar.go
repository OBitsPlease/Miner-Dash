package server

import (
	"context"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type calendarEvent struct {
	Days        int    `json:"days"`
	Date        string `json:"date"`
	EventType   string `json:"event_type"`
	Label       string `json:"label"`
	Name        string `json:"name"`
	Symbol      string `json:"symbol"`
	IconURL     string `json:"icon_url"`
	CoinURL     string `json:"coin_url"`
	Page        string `json:"page"`
	BlockInfo   string `json:"block_info"`
	Description string `json:"description"`
}

type calendarPayload struct {
	Upcoming  []calendarEvent `json:"upcoming"`
	Past      []calendarEvent `json:"past"`
	UpdatedAt time.Time       `json:"updated_at"`
}

var (
	calendarBoxPattern         = regexp.MustCompile(`<div class="info-box box-event box-event-([a-z_]+)"`)
	calendarDatePattern        = regexp.MustCompile(`class="info-box-text"[^>]*>([^<]*)<`)
	calendarDaysPattern        = regexp.MustCompile(`>\s*(-?\d+)\s*<br>\s*Days\s*<`)
	calendarLabelPattern       = regexp.MustCompile(`(?s)<small class="label[^"]*"[^>]*>(.*?)</small>`)
	calendarIconPattern        = regexp.MustCompile(`data-src="([^"]+)"`)
	calendarAltPattern         = regexp.MustCompile(`alt="([^"]*)"`)
	calendarLinkPattern        = regexp.MustCompile(`<a href="([^"]+)"[^>]*>([^<]+)</a>`)
	calendarBlockPattern       = regexp.MustCompile(`>\s*(Block\s*:[^<]*)<`)
	calendarDescriptionPattern = regexp.MustCompile(`(?s)<span>(.*?)</span>\s*</div>`)
	calendarSymbolPattern      = regexp.MustCompile(`^(.*?)\s*\(([^)]+)\)\s*$`)
	htmlTagPattern             = regexp.MustCompile(`<[^>]*>`)
	whitespacePattern          = regexp.MustCompile(`\s+`)
)

func (p *discoverProvider) calendarEvents(ctx context.Context) (calendarPayload, error) {
	return p.calendar.get(ctx, func(ctx context.Context) (calendarPayload, error) {
		body, err := p.fetchText(ctx, miningPoolStatsCalendar, miningPoolStatsHome, 30*time.Second, 16<<20)
		if err != nil {
			return calendarPayload{}, err
		}
		return parseCalendarEvents(body, time.Now().UTC()), nil
	})
}

func parseCalendarEvents(body string, now time.Time) calendarPayload {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	payload := calendarPayload{Upcoming: []calendarEvent{}, Past: []calendarEvent{}, UpdatedAt: now}
	matches := calendarBoxPattern.FindAllStringSubmatchIndex(body, -1)
	for position, match := range matches {
		end := len(body)
		if position+1 < len(matches) {
			end = matches[position+1][0]
		}
		event := parseCalendarBox(body[match[2]:match[3]], body[match[1]:end])
		if event.Name == "" && event.Date == "" {
			continue
		}
		parsed, err := time.Parse("2006-01-02", event.Date)
		if err == nil {
			event.Days = int(parsed.Sub(today).Hours() / 24)
			if parsed.Before(today) {
				payload.Past = append(payload.Past, event)
				continue
			}
		}
		payload.Upcoming = append(payload.Upcoming, event)
	}
	sort.SliceStable(payload.Upcoming, func(i, j int) bool { return payload.Upcoming[i].Days < payload.Upcoming[j].Days })
	sort.SliceStable(payload.Past, func(i, j int) bool { return payload.Past[i].Days > payload.Past[j].Days })
	return payload
}

func parseCalendarBox(eventType, body string) calendarEvent {
	event := calendarEvent{EventType: eventType}
	// Events pinned to a future block height publish only a countdown, so the
	// rendered day count is the fallback when no calendar date is present.
	if match := calendarDaysPattern.FindStringSubmatch(body); len(match) == 2 {
		if days, err := strconv.Atoi(match[1]); err == nil {
			event.Days = days
		}
	}
	if match := calendarDatePattern.FindStringSubmatch(body); len(match) == 2 {
		event.Date = cleanHTMLText(match[1])
	}
	if match := calendarLabelPattern.FindStringSubmatch(body); len(match) == 2 {
		event.Label = cleanHTMLText(match[1])
	}
	if event.Label == "" {
		event.Label = strings.ReplaceAll(eventType, "_", " ")
	}
	if match := calendarIconPattern.FindStringSubmatch(body); len(match) == 2 {
		event.IconURL = miningPoolStatsHome + strings.TrimPrefix(match[1], "/")
	}
	if match := calendarAltPattern.FindStringSubmatch(body); len(match) == 2 {
		event.Name = cleanHTMLText(match[1])
	}
	if match := calendarLinkPattern.FindStringSubmatch(body); len(match) == 3 {
		event.Page = strings.TrimPrefix(match[1], "/")
		event.CoinURL = miningPoolStatsHome + event.Page
		label := cleanHTMLText(match[2])
		if parts := calendarSymbolPattern.FindStringSubmatch(label); len(parts) == 3 {
			event.Name = strings.TrimSpace(parts[1])
			event.Symbol = strings.ToUpper(strings.TrimSpace(parts[2]))
		} else if label != "" {
			event.Name = label
		}
	}
	if match := calendarBlockPattern.FindStringSubmatch(body); len(match) == 2 {
		event.BlockInfo = cleanHTMLText(match[1])
	}
	if match := calendarDescriptionPattern.FindStringSubmatch(body); len(match) == 2 {
		event.Description = cleanHTMLText(match[1])
	}
	return event
}

func cleanHTMLText(value string) string {
	text := htmlTagPattern.ReplaceAllString(value, " ")
	text = html.UnescapeString(text)
	text = strings.ReplaceAll(text, "\u00a0", " ")
	return strings.TrimSpace(whitespacePattern.ReplaceAllString(text, " "))
}
