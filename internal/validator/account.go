package validator

// CheckNewAccount records the field errors for a new account's name, email
// and password. Signup and "www create-user" both use it.
func (v *Validator) CheckNewAccount(name, email, password string) {
	v.CheckField(NotBlank(name), "name", "Name is required")
	v.CheckField(NotBlank(email), "email", "Email is required")
	v.CheckField(Matches(email, EmailRX), "email", "Email must be a valid email address")
	v.CheckField(NotBlank(password), "password", "Password is required")
	v.CheckField(MinChars(password, 8), "password", "Password must be at least 8 characters long")
	v.CheckField(MaxChars(password, 256), "password", "Password must not be more than 256 characters long")
}
