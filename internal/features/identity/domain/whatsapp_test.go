package domain

import "testing"

func TestNormalizeWhatsApp(t *testing.T) {
	for in, want := range map[string]string{"987 654 321": "51987654321", "+51 987-654-321": "51987654321", "": "", "+1 (415) 555-0100": "14155550100"} {
		if got, e := NormalizeWhatsApp(in); e != nil || got != want {
			t.Fatal(in, got, e)
		}
	}
	for _, bad := range []string{"12345", "98765abc1", "0123456789", "1234567890123456"} {
		if _, e := NormalizeWhatsApp(bad); e == nil {
			t.Fatal("debe rechazar", bad)
		}
	}
}
