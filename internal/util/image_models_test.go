package util

import "testing"

func TestImageGenerationModelSetExcludesTextModels(t *testing.T) {
	for _, model := range []string{ImageModelAuto, ImageModelGPT, ImageModelCodex} {
		if !IsImageGenerationModel(model) {
			t.Fatalf("IsImageGenerationModel(%q) = false, want true", model)
		}
	}

	for _, model := range []string{"gpt-5", "gpt-5-5", "gpt-5.4-mini", "gpt-5-5-thinking", "gpt-6"} {
		if IsImageGenerationModel(model) {
			t.Fatalf("IsImageGenerationModel(%q) = true, want false", model)
		}
	}
}

func TestResponsesImageToolModelsIncludeTextModels(t *testing.T) {
	for _, model := range []string{
		ImageModelAuto,
		ImageModelGPT,
		ImageModelCodex,
		"gpt-5",
		"gpt-5-5",
		"gpt-5.4-mini",
		"gpt-5-5-thinking",
		"gpt-6",
	} {
		if !IsResponsesImageToolModel(model) {
			t.Fatalf("IsResponsesImageToolModel(%q) = false, want true", model)
		}
	}
	if IsImageGenerationModel("gpt-5-5") {
		t.Fatalf("IsImageGenerationModel(%q) = true, want false for /v1/images routes", "gpt-5-5")
	}
	if IsImageGenerationModel("gpt-6") {
		t.Fatalf("IsImageGenerationModel(%q) = true, want false for /v1/images routes", "gpt-6")
	}
}

func TestResponsesImageToolModelsRejectCodenames(t *testing.T) {
	for _, model := range []string{"", "i-5-mini-m", "n7jupd", "o3"} {
		if IsResponsesImageToolModel(model) {
			t.Fatalf("IsResponsesImageToolModel(%q) = true, want false", model)
		}
	}
}

// ModelList 只列本客户端自有路由的图片模型：文本模型来自上游实时列表
// （protocol.Engine.ListChatModels），在本地重复声明一份就会随上游改名过期。
func TestModelListOnlyCarriesClientRoutedImageModels(t *testing.T) {
	wantOrder := []string{ImageModelGPT, ImageModelCodex, ImageModelAuto}
	gotOrder := ModelList()
	if len(gotOrder) != len(wantOrder) {
		t.Fatalf("len(ModelList()) = %d, want %d: %#v", len(gotOrder), len(wantOrder), gotOrder)
	}
	for index, want := range wantOrder {
		if gotOrder[index] != want {
			t.Fatalf("ModelList()[%d] = %q, want %q; full list: %#v", index, gotOrder[index], want, gotOrder)
		}
	}
}

// IsOptionalModel 决定上游实时模型能不能进下拉：既要放行 gpt-6 这类真实模型，
// 又要挡住 i-5-mini-m 这类只出现在服务端元数据里的内部代号。
func TestIsOptionalModelAcceptsUserModelsAndRejectsCodenames(t *testing.T) {
	for _, model := range []string{
		"gpt-5",
		"gpt-5-5",
		"gpt-5-5-mini",
		"gpt-5-6",
		"gpt-6",
		"gpt-6-mini",
		"gpt-5.1",
		"gpt-4o",
		"gpt-4o-mini",
	} {
		if !IsOptionalModel(model) {
			t.Fatalf("IsOptionalModel(%q) = false, want true", model)
		}
	}

	for _, model := range []string{
		"",
		"auto",
		"gpt-image-2",
		"codex-gpt-image-2",
		"i-5-mini-m",
		"n7jupd",
		"gpt",
		"gpt-",
		"gpt-6-",
		"gpt-6-Mini",
		"o3",
		"gpt 6",
	} {
		if IsOptionalModel(model) {
			t.Fatalf("IsOptionalModel(%q) = true, want false", model)
		}
	}
}
