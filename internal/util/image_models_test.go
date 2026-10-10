package util

import "testing"

func TestImageGenerationModelSetExcludesTextModels(t *testing.T) {
	for _, model := range []string{ImageModelAuto, ImageModelGPT, ImageModelCodex} {
		if !IsImageGenerationModel(model) {
			t.Fatalf("IsImageGenerationModel(%q) = false, want true", model)
		}
	}

	for _, model := range []string{
		ImageModelGPT5,
		ImageModelGPT53Mini,
		ImageModelGPT54,
		ImageModelGPT55,
		ImageModelGPT55Mini,
		ImageModelGPT56,
		ImageModelGPT56Mini,
		ImageModelGPT6,
	} {
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
		ImageModelGPT5,
		ImageModelGPT53Mini,
		ImageModelGPT54,
		ImageModelGPT55,
		ImageModelGPT55Mini,
		ImageModelGPT56,
		ImageModelGPT56Mini,
		ImageModelGPT6,
	} {
		if !IsResponsesImageToolModel(model) {
			t.Fatalf("IsResponsesImageToolModel(%q) = false, want true", model)
		}
	}
	if IsImageGenerationModel(ImageModelGPT55) {
		t.Fatalf("IsImageGenerationModel(%q) = true, want false for /v1/images routes", ImageModelGPT55)
	}
	if IsImageGenerationModel(ImageModelGPT56) {
		t.Fatalf("IsImageGenerationModel(%q) = true, want false for /v1/images routes", ImageModelGPT56)
	}
}

func TestModelListIncludesTextAndImageModels(t *testing.T) {
	wantOrder := []string{
		ImageModelGPT,
		ImageModelCodex,
		ImageModelAuto,
		ImageModelGPT5,
		ImageModelGPT53Mini,
		ImageModelGPT54,
		ImageModelGPT55,
		ImageModelGPT55Mini,
		ImageModelGPT56,
		ImageModelGPT56Mini,
		ImageModelGPT6,
	}
	gotOrder := ModelList()
	if len(gotOrder) != len(wantOrder) {
		t.Fatalf("len(ModelList()) = %d, want %d: %#v", len(gotOrder), len(wantOrder), gotOrder)
	}
	for index, want := range wantOrder {
		if gotOrder[index] != want {
			t.Fatalf("ModelList()[%d] = %q, want %q; full list: %#v", index, gotOrder[index], want, gotOrder)
		}
	}

	got := map[string]struct{}{}
	for _, model := range ModelList() {
		got[model] = struct{}{}
	}

	for _, model := range []string{
		ImageModelAuto,
		ImageModelGPT,
		ImageModelCodex,
		ImageModelGPT5,
		ImageModelGPT53Mini,
		ImageModelGPT54,
		ImageModelGPT55,
		ImageModelGPT55Mini,
		ImageModelGPT56,
		ImageModelGPT56Mini,
		ImageModelGPT6,
	} {
		if _, ok := got[model]; !ok {
			t.Fatalf("ModelList() missing %q", model)
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
