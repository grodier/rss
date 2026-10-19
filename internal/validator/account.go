package validator

// CheckNewAccount records the field errors for a new account's name, email
// and password. Signup and "www create-user" both use it.
func (v *Validator) CheckNewAccount(name, email, password string) {
	v.CheckField(NotBlank(name), "name", "Name is required")
	v.CheckField(NotBlank(email), "email", "Email is required")
	v.CheckField(Matches(email, EmailRX), "email", "Email must be a valid email address")
	v.CheckNewPassword("password", password, email)
}
