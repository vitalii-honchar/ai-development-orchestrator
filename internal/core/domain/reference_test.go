package domain

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestReference(t *testing.T) {
	t.Parallel()
	for _, origin := range []Origin{OriginProcess, OriginResource} {
		model, err := NewReference(WithReferenceOrigin(origin), WithReferenceValue("source"))
		if err != nil || model.ToString() != string(origin)+":source" {
			t.Fatalf("reference: %+v, %v", model, err)
		}
		data, err := json.Marshal(model)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != `{"origin":"`+string(origin)+`","value":"source"}` {
			t.Fatalf("unexpected reference JSON: %s", data)
		}
		var restored Reference
		if err := json.Unmarshal(data, &restored); err != nil || !reflect.DeepEqual(model, &restored) {
			t.Fatalf("reference JSON round trip: %+v, %v", restored, err)
		}
	}
	for _, option := range []ReferenceOpt{
		WithReferenceOrigin(""),
		WithReferenceOrigin("unknown"),
		WithReferenceValue(""),
		WithReferenceValue(" "),
		WithReferenceValue(string([]byte{0xff})),
	} {
		if model, err := NewReference(
			WithReferenceOrigin(OriginProcess),
			WithReferenceValue("source"),
			option,
		); err == nil || model != nil {
			t.Fatalf("accepted invalid reference: %+v, %v", model, err)
		}
	}
}
