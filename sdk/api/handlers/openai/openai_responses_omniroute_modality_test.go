package openai

import "testing"

func TestOmniRouteTextRequestHasImage(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    bool
	}{
		{
			name:    "omni route image part",
			payload: `{"model":"omni-gpt-luna","input":[{"role":"user","content":[{"type":"input_text","text":"hi"},{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}]}`,
			want:    true,
		},
		{
			name:    "omni route text mentioning input image",
			payload: `{"model":"omni-gpt-luna","input":[{"role":"user","content":[{"type":"input_text","text":"the literal input_image is harmless text"}]}]}`,
			want:    false,
		},
		{
			name:    "native route image remains outside omni policy",
			payload: `{"model":"gpt-5.6-sol","input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}]}`,
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := omniRouteTextRequestHasImage([]byte(tt.payload)); got != tt.want {
				t.Fatalf("omniRouteTextRequestHasImage() = %v, want %v", got, tt.want)
			}
		})
	}
}
