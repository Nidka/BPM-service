package domain

import (
	"strings"
	"testing"
)

func TestDeviceRequiresExplicitPrivateBinding(t *testing.T) {
	v := DeviceInput{ID: "00000000-0000-0000-0000-000000000001", InstallationID: "00000000-0000-0000-0000-000000000002", Platform: "ANDROID", PushToken: "token"}
	if err := v.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*DeviceInput){func(v *DeviceInput) { v.PushToken = "" }, func(v *DeviceInput) { v.PushToken = strings.Repeat("x", 4097) }, func(v *DeviceInput) { v.Platform = "PHONE" }, func(v *DeviceInput) { v.ID = "other" }, func(v *DeviceInput) { v.InstallationID = "../" }} {
		bad := v
		change(&bad)
		if bad.Validate() == nil {
			t.Fatal("binding inválido aceptado")
		}
	}
	if !ValidUID(strings.Repeat("é", 128)) || ValidUID(strings.Repeat("é", 129)) || ValidUID("x\n") {
		t.Fatal("UID Unicode inválido")
	}
}
