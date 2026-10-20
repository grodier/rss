package validator

// CheckNewAccount records the field errors for a new account's name, email
// and password. Signup and "www create-user" both use it.
func (v *Validator) CheckNewAccount(name, email, password string) {
	v.CheckName(name)
	v.CheckField(NotBlank(email), "email", "Email is required")
	v.CheckField(Matches(email, EmailRX), "email", "Email must be a valid email address")
	v.CheckNewPassword("password", password, email)
}

// CheckName records the error for an account's name: required, at most 100
// characters. Callers trim it first.
func (v *Validator) CheckName(name string) {
	v.CheckField(NotBlank(name), "name", "Name is required")
	v.CheckField(MaxChars(name, 100), "name", "Name must not be more than 100 characters long")
}
