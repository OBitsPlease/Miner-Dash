package agent

import "testing"

func TestIsUbuntuFamily(t *testing.T) {
	tests := []struct {
		name    string
		release string
		want    bool
	}{
		{name: "ubuntu", release: "ID=ubuntu\n", want: true},
		{name: "ubuntu derivative", release: "ID=linuxmint\nID_LIKE=\"ubuntu debian\"\n", want: true},
		{name: "debian", release: "ID=debian\nID_LIKE=debian\n", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isUbuntuFamily(test.release); got != test.want {
				t.Fatalf("isUbuntuFamily() = %v, want %v", got, test.want)
			}
		})
	}
}
