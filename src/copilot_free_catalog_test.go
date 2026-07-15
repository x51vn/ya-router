package yarouter

import (
	"reflect"
	"testing"
)

const supportedModelsHTMLFixture = `
<html><body>
  <h2 id="supported-ai-models-per-copilot-plan">Supported AI models per Copilot plan</h2>
  <div class="ghd-tool rowheaders">
    <table>
      <thead>
        <tr>
          <th scope="col">Available models in chat</th>
          <th scope="col">Copilot Free</th>
          <th scope="col">Copilot Student</th>
          <th scope="col">Copilot Pro</th>
        </tr>
      </thead>
      <tbody>
        <tr>
          <th scope="row">GPT-4.1</th>
          <td><svg aria-label="Included"></svg></td>
          <td><svg aria-label="Included"></svg></td>
          <td><svg aria-label="Included"></svg></td>
        </tr>
        <tr>
          <th scope="row">GPT-4o</th>
          <td><svg aria-label="Included"></svg></td>
          <td><svg aria-label="Included"></svg></td>
          <td><svg aria-label="Included"></svg></td>
        </tr>
        <tr>
          <th scope="row">GPT-5 mini</th>
          <td><svg aria-label="Included"></svg></td>
          <td><svg aria-label="Included"></svg></td>
          <td><svg aria-label="Included"></svg></td>
        </tr>
        <tr>
          <th scope="row">GPT-5.4 mini</th>
          <td><svg aria-label="Not included"></svg></td>
          <td><svg aria-label="Included"></svg></td>
          <td><svg aria-label="Included"></svg></td>
        </tr>
        <tr>
          <th scope="row">Raptor mini</th>
          <td><svg aria-label="Included"></svg></td>
          <td><svg aria-label="Included"></svg></td>
          <td><svg aria-label="Included"></svg></td>
        </tr>
        <tr>
          <th scope="row">Goldeneye</th>
          <td><svg aria-label="Included"></svg></td>
          <td><svg aria-label="Not included"></svg></td>
          <td><svg aria-label="Not included"></svg></td>
        </tr>
      </tbody>
    </table>
  </div>
</body></html>
`

const billingHTMLFixture = `
<html><body>
  <h2 id="model-multipliers">Model multipliers</h2>
  <div class="ghd-tool rowheaders">
    <table>
      <thead>
        <tr>
          <th scope="col">Model</th>
          <th scope="col">Multiplier for <strong>paid plans</strong></th>
          <th scope="col">Multiplier for <strong>Copilot Free</strong></th>
        </tr>
      </thead>
      <tbody>
        <tr><th scope="row">GPT-4.1</th><td>0</td><td>1</td></tr>
        <tr><th scope="row">GPT-4o</th><td>0</td><td>1</td></tr>
        <tr><th scope="row">GPT-5 mini</th><td>0</td><td>1</td></tr>
        <tr><th scope="row">GPT-5.4 mini</th><td>0.33</td><td>Not applicable</td></tr>
        <tr><th scope="row">Raptor mini</th><td>0</td><td>1</td></tr>
        <tr><th scope="row">Goldeneye</th><td>Not applicable</td><td>1</td></tr>
      </tbody>
    </table>
  </div>
</body></html>
`

func TestCopilotFreeCatalog_ComputeEligibleDocModels(t *testing.T) {
	freePlanModels, err := parseCopilotPlanFreeModels(supportedModelsHTMLFixture)
	if err != nil {
		t.Fatalf("parseCopilotPlanFreeModels: %v", err)
	}
	zeroPremiumModels, err := parseZeroPremiumModels(billingHTMLFixture)
	if err != nil {
		t.Fatalf("parseZeroPremiumModels: %v", err)
	}

	got := intersectNormalizedModelNames(freePlanModels, zeroPremiumModels)
	want := []string{"gpt 4 1", "gpt 4o", "gpt 5 mini", "raptor mini"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("eligible doc models = %v, want %v", got, want)
	}
}

func TestResolveEffectiveCopilotFreeModels_PrefersVisibleCandidate(t *testing.T) {
	docEligible := []string{"gpt 4 1", "gpt 4o", "gpt 5 mini", "raptor mini"}
	upstream := &ModelList{
		Object: "list",
		Data: []Model{
			{ID: "gpt-4.1", Name: "GPT-4.1", ModelPickerEnabled: true},
			{ID: "gpt-4o", Name: "GPT-4o", ModelPickerEnabled: true},
			{ID: "gpt-5-mini", Name: "GPT-5 mini", ModelPickerEnabled: true},
			{ID: "oswe-vscode-secondary", Name: "Raptor mini (Preview)", ModelPickerEnabled: false, Preview: true},
			{ID: "oswe-vscode-prime", Name: "Raptor mini (Preview)", ModelPickerEnabled: true, Preview: true},
			{ID: "goldeneye-free-auto", Name: "Goldeneye", ModelPickerEnabled: false, Preview: true},
		},
	}

	got := resolveEffectiveCopilotFreeModels(docEligible, upstream)
	want := []string{"gpt-4.1", "gpt-4o", "gpt-5-mini", "oswe-vscode-prime"}

	var gotIDs []string
	for _, model := range got {
		gotIDs = append(gotIDs, model.ID)
	}

	if !reflect.DeepEqual(gotIDs, want) {
		t.Fatalf("effective upstream IDs = %v, want %v", gotIDs, want)
	}
}
