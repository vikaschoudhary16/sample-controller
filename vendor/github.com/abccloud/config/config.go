package config

// A Config provides service configuration for service clients
type Config struct {
	// cloud credentials
	Credentials Value
}

// A Value is the credentials value for individual credential fields.
type Value struct {
	// Access key ID
	AccessKeyID string

	// Secret Access Key
	SecretAccessKey string
}
