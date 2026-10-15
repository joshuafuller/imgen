package fal

import (
	"fmt"
	"testing"

	"github.com/joshuafuller/imgen/internal/provider"
)

func TestSetSize(t *testing.T) {
	for _, tc := range []struct {
		model, ratio, size string
		wantKey            string
		want               any
	}{
		{"fal-ai/flux/dev", "1:1", "", "image_size", "square_hd"},
		{"fal-ai/flux/dev", "9:16", "", "image_size", "portrait_16_9"},
		{"fal-ai/flux/dev", "21:9", "", "image_size", map[string]any{"width": 1024, "height": 432}},
		{"fal-ai/flux/dev", "", "640x480", "image_size", map[string]any{"width": 640, "height": 480}},
		{"fal-ai/flux-pro/kontext", "21:9", "", "aspect_ratio", "21:9"},
		{"fal-ai/flux-pro/kontext/max", "", "1600x900", "aspect_ratio", "16:9"},
	} {
		body := map[string]any{}
		setSize(body, provider.Request{Model: tc.model, AspectRatio: tc.ratio, Size: tc.size})
		if fmt.Sprint(body[tc.wantKey]) != fmt.Sprint(tc.want) || len(body) != 1 {
			t.Errorf("%+v -> %v", tc, body)
		}
	}
	body := map[string]any{}
	setSize(body, provider.Request{Model: "fal-ai/flux/dev"})
	if len(body) != 0 {
		t.Errorf("no size requested, body = %v", body)
	}
}
