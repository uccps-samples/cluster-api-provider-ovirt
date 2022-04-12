package ovirt

type Creds struct {
	URL      string
	Username string
	Password string
	CAFile   string
	Insecure bool
	CABundle string
}
