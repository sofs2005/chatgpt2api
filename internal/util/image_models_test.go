package util

import "testing"

func TestImageGenerationModelSetExcludesTextModels(t *testing.T) {
	for _, model := range []string{ImageModelAuto, ImageModelGPT, ImageModelCodex} {
		if !IsImageGenerationModel(model) {
			t.Fatalf("IsImageGenerationModel(%q) = false, want true", model)
		}
	}

	for _, model := range []string{
		ImageModelGPTMini,
		ImageModelGPT53Mini,
		ImageModelGPT5,
		ImageModelGPT51,
		ImageModelGPT52,
		ImageModelGPT53,
		ImageModelGPT54,
		ImageModelGPT55,
	} {
		if IsImageGenerationModel(model) {
			t.Fatalf("IsImageGenerationModel(%q) = true, want false", model)
		}
	}
}

func TestResponsesImageToolModelsIncludeTextModels(t *testing.T) {
	for _, model := range []string{ImageModelAuto, ImageModelGPT, ImageModelCodex, ImageModelGPT5, ImageModelGPT54, ImageModelGPT55} {
		if !IsResponsesImageToolModel(model) {
			t.Fatalf("IsResponsesImageToolModel(%q) = false, want true", model)
		}
	}
	if IsImageGenerationModel(ImageModelGPT55) {
		t.Fatalf("IsImageGenerationModel(%q) = true, want false for /v1/images routes", ImageModelGPT55)
	}
}

func TestModelListIncludesTextAndImageModels(t *testing.T) {
	wantOrder := []string{
		ImageModelGPT,
		ImageModelCodex,
		ImageModelAuto,
		ImageModelGPTMini,
		ImageModelGPT53Mini,
		ImageModelGPT5,
		ImageModelGPT51,
		ImageModelGPT52,
		ImageModelGPT53,
		ImageModelGPT54,
		ImageModelGPT55,
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
		ImageModelGPTMini,
		ImageModelGPT53Mini,
		ImageModelGPT5,
		ImageModelGPT53,
		ImageModelGPT54,
		ImageModelGPT55,
	} {
		if _, ok := got[model]; !ok {
			t.Fatalf("ModelList() missing %q", model)
		}
	}
}
