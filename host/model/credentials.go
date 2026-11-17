package model

// DeployOnly reports the scope of a stored deployment credential.
func (u User) DeployOnly() bool { return u.KeyScope == "deploy" }
