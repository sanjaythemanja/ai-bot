package calendar

import (
	"context"
	"testing"
	"time"

	"github.com/sanjayrj/ai-bot/internal/provider/mock"
)

func TestFreeSlots(t *testing.T) {
	loc := time.UTC
	ws := mock.New(time.Date(2030, 1, 7, 8, 0, 0, 0, loc)) // future date: "now" never clips the range
	p := New(ws.Calendar(), loc)
	slots, err := p.FreeSlots(context.Background(), "2030-01-07", 60, "09:00", "18:00")
	if err != nil {
		t.Fatal(err)
	}
	// Busy 9:30-10:15, 11:00-11:30, 12:30-13:30, 15:00-15:45 → 1h+ gaps: 11:30, 13:30, 15:45
	if len(slots) != 3 || slots[0].Start.Format("15:04") != "11:30" || slots[2].Start.Format("15:04") != "15:45" {
		t.Fatalf("unexpected slots: %+v", slots)
	}
}
