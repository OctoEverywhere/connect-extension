package main

import "testing"

func TestReleaseVersionAcceptedByHostProtocol(t *testing.T) {
	if _, _, _, err := release.Parse(); err != nil {
		t.Fatalf("embedded release version is rejected by the host wire protocol: %v", err)
	}
}
