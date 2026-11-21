package hackhttp

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/app/events"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

func checkHackText(w http.ResponseWriter, s, field string, min, max int) (string, bool) {
	value, e := events.Text(s, field, min, max)
	if e != nil {
		httpx.WriteError(w, 400, e.Code, e.Message)
		return "", false
	}
	return value, true
}

func checkHackLine(w http.ResponseWriter, s, field string, min, max int) (string, bool) {
	value, e := events.Line(s, field, min, max)
	if e != nil {
		httpx.WriteError(w, 400, e.Code, e.Message)
		return "", false
	}
	return value, true
}

func rfc3339UTC(t sql.NullTime) any {
	if !t.Valid {
		return nil
	}
	return t.Time.UTC().Format(time.RFC3339)
}

func rfc3339Time(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// validateSignupAnswersValue is events.SignupAnswers with the error as an
// events.Error, for validators the events service runs under its lock.
func validateSignupAnswersValue(questions []hackmodel.SignupQuestion, in map[string]string) ([]hackmodel.SignupAnswer, error) {
	answers, e := events.SignupAnswers(questions, in)
	if e != nil {
		return nil, &events.Error{Code: e.Code, Message: e.Message}
	}
	return answers, nil
}
