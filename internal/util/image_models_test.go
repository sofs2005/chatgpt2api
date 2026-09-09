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
	} {
		if _, ok := got[model]; !ok {
			t.Fatalf("ModelList() missing %q", model)
		}
	}
}
