package docs

// The marks a site shows: Heron's icon in the top bar and as the page's
// icon where no theme gives a logo, each in its version for light
// backgrounds in the light theme; a theme's logo, and its light version,
// in place of the mark.

// logoMark is the top bar's mark and the class of the logo link.
func (s *Site) logoMark() (mark, class string) {
	a := s.Opts.Assets
	if th := s.Theme; th != nil && th.Logo != "" {
		class = "logo"
		if th.Wordmark {
			class += " wordmark"
		}
		if th.LogoLight == "" {
			return `<img class="mark" src="` + attr(a+th.Logo) + `" height="22" alt="">`, class
		}
		return `<img class="mark mark-dark" src="` + attr(a+th.Logo) + `" height="22" alt=""><img class="mark mark-light" src="` + attr(a+th.LogoLight) + `" height="22" alt="">`, class
	}
	return `<img class="mark mark-dark" src="` + attr(a+"heron-icon.svg") + `" width="20" height="20" alt=""><img class="mark mark-light" src="` + attr(a+"heron-icon-light.svg") + `" width="20" height="20" alt="">`, "logo"
}

// favicon links Heron's 16 px icon as the page's icon, its version for
// light backgrounds where the reader's system prefers light.
func (s *Site) favicon() string {
	a := s.Opts.Assets
	return `<link rel="icon" type="image/svg+xml" href="` + attr(a+"heron-16.svg") + `">` +
		`<link rel="icon" type="image/svg+xml" href="` + attr(a+"heron-16-light.svg") + `" media="(prefers-color-scheme: light)">`
}
