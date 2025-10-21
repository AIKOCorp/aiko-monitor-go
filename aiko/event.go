package aiko

// Event represents a single HTTP request/response lifecycle observation.
type Event struct {
	URL             string            `json:"url"`
	Endpoint        string            `json:"endpoint"`
	Method          string            `json:"method"`
	StatusCode      int               `json:"status_code"`
	RequestHeaders  map[string]string `json:"request_headers"`
	RequestBody     interface{}       `json:"request_body"`
	ResponseHeaders map[string]string `json:"response_headers"`
	ResponseBody    interface{}       `json:"response_body"`
	DurationMS      int64             `json:"duration_ms"`
}

func redactEvent(evt Event) Event {
	return Event{
		URL:             evt.URL,
		Endpoint:        evt.Endpoint,
		Method:          evt.Method,
		StatusCode:      evt.StatusCode,
		RequestHeaders:  redactHeaders(evt.RequestHeaders),
		RequestBody:     redactValue(evt.RequestBody),
		ResponseHeaders: redactHeaders(evt.ResponseHeaders),
		ResponseBody:    redactValue(evt.ResponseBody),
		DurationMS:      evt.DurationMS,
	}
}
