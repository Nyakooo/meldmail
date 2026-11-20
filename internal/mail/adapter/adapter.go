package adapter

import "time"

type Preset struct {
	IMAPHost string
	IMAPPort int
	IMAPTLS  string
	SMTPHost string
	SMTPPort int
	SMTPTLS  string
}

func Provider(name string) (Preset, bool) {
	presets := map[string]Preset{
		"gmail":   {"imap.gmail.com", 993, "implicit", "smtp.gmail.com", 465, "implicit"},
		"163":     {"imap.163.com", 993, "implicit", "smtp.163.com", 465, "implicit"},
		"126":     {"imap.126.com", 993, "implicit", "smtp.126.com", 465, "implicit"},
		"qq":      {"imap.qq.com", 993, "implicit", "smtp.qq.com", 465, "implicit"},
		"outlook": {"outlook.office365.com", 993, "implicit", "smtp.office365.com", 587, "starttls"},
	}
	p, ok := presets[name]
	return p, ok
}
func NeedsID(provider string) bool { return provider == "163" || provider == "126" }
func IdleInterval(provider string) time.Duration {
	if provider == "qq" {
		return 12 * time.Minute
	}
	return 28 * time.Minute
}
