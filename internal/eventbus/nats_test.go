package eventbus

import "testing"

func TestSubjectForNormalizesFormType(t *testing.T) {
	t.Parallel()

	got := SubjectFor(DiscoverySubject, "10-K/A")

	want := "edgar.filing.discovered.10-K_A"
	if got != want {
		t.Fatalf("SubjectFor() = %q, want %q", got, want)
	}
}
