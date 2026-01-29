package antigravity

import (
	"testing"

	"github.com/kuzerno1/multi-claude-proxy/pkg/types"
)

func TestConvertImageRequestToGoogle(t *testing.T) {
	t.Run("basic prompt conversion", func(t *testing.T) {
		req := &types.ImageGenerationRequest{
			Prompt: "a sunset over mountains",
			Model:  "gemini-3-pro-image",
			Count:  1,
		}

		result := ConvertImageRequestToGoogle(req, "test-project")

		if result["project"] != "test-project" {
			t.Errorf("expected project 'test-project', got %v", result["project"])
		}
		if result["model"] != "gemini-3-pro-image" {
			t.Errorf("expected model 'gemini-3-pro-image', got %v", result["model"])
		}
		if result["userAgent"] != "antigravity" {
			t.Errorf("expected userAgent 'antigravity', got %v", result["userAgent"])
		}
		if result["requestType"] != "agent" {
			t.Errorf("expected requestType 'agent', got %v", result["requestType"])
		}

		// Check contents structure
		googleReq, ok := result["request"].(map[string]interface{})
		if !ok {
			t.Fatal("request field should be a map")
		}
		contents, ok := googleReq["contents"].([]interface{})
		if !ok || len(contents) == 0 {
			t.Fatal("contents should be a non-empty array")
		}
		firstContent := contents[0].(map[string]interface{})
		if firstContent["role"] != "user" {
			t.Errorf("expected role 'user', got %v", firstContent["role"])
		}
		parts := firstContent["parts"].([]interface{})
		if len(parts) != 1 {
			t.Errorf("expected 1 part, got %d", len(parts))
		}
		firstPart := parts[0].(map[string]interface{})
		if firstPart["text"] != "a sunset over mountains" {
			t.Errorf("expected prompt text, got %v", firstPart["text"])
		}

		// Check generationConfig has responseModalities
		genConfig := googleReq["generationConfig"].(map[string]interface{})
		modalities, ok := genConfig["responseModalities"].([]string)
		if !ok || len(modalities) != 1 || modalities[0] != "IMAGE" {
			t.Errorf("expected responseModalities [\"IMAGE\"], got %v", genConfig["responseModalities"])
		}
	})

	t.Run("with aspect ratio", func(t *testing.T) {
		req := &types.ImageGenerationRequest{
			Prompt:      "a cat",
			Model:       "gemini-3-pro-image",
			AspectRatio: "16:9",
			Count:       1,
		}

		result := ConvertImageRequestToGoogle(req, "test-project")

		googleReq := result["request"].(map[string]interface{})
		genConfig := googleReq["generationConfig"].(map[string]interface{})

		// Check responseModalities
		modalities, ok := genConfig["responseModalities"].([]string)
		if !ok || len(modalities) != 1 || modalities[0] != "IMAGE" {
			t.Errorf("expected responseModalities [\"IMAGE\"], got %v", genConfig["responseModalities"])
		}

		// Check imageConfig
		imageConfig, ok := genConfig["imageConfig"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected imageConfig in generationConfig")
		}
		if imageConfig["aspectRatio"] != "16:9" {
			t.Errorf("expected aspectRatio '16:9', got %v", imageConfig["aspectRatio"])
		}
	})

	t.Run("with count", func(t *testing.T) {
		req := &types.ImageGenerationRequest{
			Prompt: "a dog",
			Model:  "gemini-3-pro-image",
			Count:  4,
		}

		result := ConvertImageRequestToGoogle(req, "test-project")

		googleReq := result["request"].(map[string]interface{})
		genConfig := googleReq["generationConfig"].(map[string]interface{})
		if genConfig["candidateCount"] != 4 {
			t.Errorf("expected candidateCount 4, got %v", genConfig["candidateCount"])
		}
	})

	t.Run("with session ID", func(t *testing.T) {
		req := &types.ImageGenerationRequest{
			Prompt:    "a character",
			Model:     "gemini-3-pro-image",
			SessionID: "session-123",
			Count:     1,
		}

		result := ConvertImageRequestToGoogle(req, "test-project")

		if result["sessionId"] != "session-123" {
			t.Errorf("expected sessionId 'session-123', got %v", result["sessionId"])
		}
	})

	t.Run("with input image for editing", func(t *testing.T) {
		req := &types.ImageGenerationRequest{
			Prompt:     "change the sky to sunset",
			Model:      "gemini-3-pro-image",
			InputImage: "base64encodedimage==",
			Count:      1,
		}

		result := ConvertImageRequestToGoogle(req, "test-project")

		googleReq := result["request"].(map[string]interface{})
		contents := googleReq["contents"].([]interface{})
		firstContent := contents[0].(map[string]interface{})
		parts := firstContent["parts"].([]interface{})

		if len(parts) != 2 {
			t.Fatalf("expected 2 parts (text + image), got %d", len(parts))
		}

		imagePart := parts[1].(map[string]interface{})
		inlineData := imagePart["inlineData"].(map[string]interface{})
		if inlineData["mimeType"] != "image/png" {
			t.Errorf("expected mimeType 'image/png', got %v", inlineData["mimeType"])
		}
		if inlineData["data"] != "base64encodedimage==" {
			t.Errorf("expected image data, got %v", inlineData["data"])
		}
	})
}

func TestConvertGoogleImageResponse(t *testing.T) {
	t.Run("successful response with single image", func(t *testing.T) {
		googleResp := map[string]interface{}{
			"candidates": []interface{}{
				map[string]interface{}{
					"content": map[string]interface{}{
						"parts": []interface{}{
							map[string]interface{}{
								"inlineData": map[string]interface{}{
									"mimeType": "image/png",
									"data":     "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==",
								},
							},
						},
					},
				},
			},
		}

		resp, err := ConvertGoogleImageResponse(googleResp, "gemini-3-pro-image")

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Type != "image_generation" {
			t.Errorf("expected type 'image_generation', got %s", resp.Type)
		}
		if resp.Model != "gemini-3-pro-image" {
			t.Errorf("expected model 'gemini-3-pro-image', got %s", resp.Model)
		}
		if len(resp.Images) != 1 {
			t.Fatalf("expected 1 image, got %d", len(resp.Images))
		}
		if resp.Images[0].MediaType != "image/png" {
			t.Errorf("expected mediaType 'image/png', got %s", resp.Images[0].MediaType)
		}
		if resp.Images[0].Index != 0 {
			t.Errorf("expected index 0, got %d", resp.Images[0].Index)
		}
	})

	t.Run("response with multiple images", func(t *testing.T) {
		googleResp := map[string]interface{}{
			"candidates": []interface{}{
				map[string]interface{}{
					"content": map[string]interface{}{
						"parts": []interface{}{
							map[string]interface{}{
								"inlineData": map[string]interface{}{
									"mimeType": "image/png",
									"data":     "image1data",
								},
							},
						},
					},
				},
				map[string]interface{}{
					"content": map[string]interface{}{
						"parts": []interface{}{
							map[string]interface{}{
								"inlineData": map[string]interface{}{
									"mimeType": "image/jpeg",
									"data":     "image2data",
								},
							},
						},
					},
				},
			},
		}

		resp, err := ConvertGoogleImageResponse(googleResp, "gemini-3-pro-image")

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(resp.Images) != 2 {
			t.Fatalf("expected 2 images, got %d", len(resp.Images))
		}
		if resp.Images[0].Index != 0 {
			t.Errorf("expected first image index 0, got %d", resp.Images[0].Index)
		}
		if resp.Images[1].Index != 1 {
			t.Errorf("expected second image index 1, got %d", resp.Images[1].Index)
		}
	})

	t.Run("nested response structure", func(t *testing.T) {
		googleResp := map[string]interface{}{
			"response": map[string]interface{}{
				"candidates": []interface{}{
					map[string]interface{}{
						"content": map[string]interface{}{
							"parts": []interface{}{
								map[string]interface{}{
									"inlineData": map[string]interface{}{
										"mimeType": "image/png",
										"data":     "nestedimage",
									},
								},
							},
						},
					},
				},
			},
		}

		resp, err := ConvertGoogleImageResponse(googleResp, "gemini-3-pro-image")

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(resp.Images) != 1 {
			t.Fatalf("expected 1 image, got %d", len(resp.Images))
		}
	})

	t.Run("error on empty candidates", func(t *testing.T) {
		googleResp := map[string]interface{}{
			"candidates": []interface{}{},
		}

		_, err := ConvertGoogleImageResponse(googleResp, "gemini-3-pro-image")

		if err == nil {
			t.Fatal("expected error for empty candidates")
		}
		if err.Error() != "no candidates in image response" {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("error on no images in parts", func(t *testing.T) {
		googleResp := map[string]interface{}{
			"candidates": []interface{}{
				map[string]interface{}{
					"content": map[string]interface{}{
						"parts": []interface{}{
							map[string]interface{}{
								"text": "some text, not an image",
							},
						},
					},
				},
			},
		}

		_, err := ConvertGoogleImageResponse(googleResp, "gemini-3-pro-image")

		if err == nil {
			t.Fatal("expected error for no images")
		}
		if err.Error() != "no images found in response" {
			t.Errorf("unexpected error message: %v", err)
		}
	})
}
