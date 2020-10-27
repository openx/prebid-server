package api

import (
	"encoding/json"
	"io/ioutil"
	"net/http"

	"github.com/julienschmidt/httprouter"
	"github.com/prebid/prebid-server/config"
	"github.com/prebid/prebid-server/stored_requests/events"
)

type eventsAPI struct {
	saves         chan events.Save
	invalidations chan events.Invalidation
}

// NewEventsAPI creates an EventProducer that generates cache events from HTTP requests.
// The returned httprouter.Handle must be registered on both POST (update) and DELETE (invalidate)
// methods and provided an `:id` param via the URL, e.g.:
//
// apiEvents, apiEventsHandler, err := NewEventsApi()
// router.POST("/stored_requests", apiEventsHandler)
// router.DELETE("/stored_requests", apiEventsHandler)
// listener := events.Listen(cache, apiEvents)
//
// The returned HTTP endpoint should not be exposed on a public network without authentication
// as it allows direct writing to the cache via Update.
func NewEventsAPI() (events.EventProducer, httprouter.Handle) {
	api := &eventsAPI{
		invalidations: make(chan events.Invalidation),
		saves:         make(chan events.Save),
	}
	return api, httprouter.Handle(api.HandleEvent)
}

func (api *eventsAPI) HandleEvent(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	if r.Method == "POST" {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Missing update data.\n"))
			return
		}

		var save saveRequestContract
		if err := json.Unmarshal(body, &save); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Invalid update.\n"))
			return
		}

		sendSaves(api.saves, config.RequestDataType, save.Requests)
		sendSaves(api.saves, config.ImpDataType, save.Imps)
		sendSaves(api.saves, config.AccountDataType, save.Accounts)
	} else if r.Method == "DELETE" {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Missing invalidation data.\n"))
			return
		}

		var invalidation invalidationRequestContract
		if err := json.Unmarshal(body, &invalidation); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Invalid invalidation.\n"))
			return
		}

		sendInvalidations(api.invalidations, config.RequestDataType, invalidation.Requests)
		sendInvalidations(api.invalidations, config.ImpDataType, invalidation.Imps)
		sendInvalidations(api.invalidations, config.AccountDataType, invalidation.Accounts)
	} else {
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api *eventsAPI) Invalidations() <-chan events.Invalidation {
	return api.invalidations
}

func (api *eventsAPI) Saves() <-chan events.Save {
	return api.saves
}

func sendInvalidations(invalidations chan<- events.Invalidation, dataType config.DataType, deletedIDs []string) {
	if len(deletedIDs) > 0 {
		invalidations <- events.Invalidation{
			DataType: dataType,
			Data:     deletedIDs,
		}
	}
}

func sendSaves(saves chan<- events.Save, dataType config.DataType, changes map[string]json.RawMessage) {
	if len(changes) > 0 {
		saves <- events.Save{DataType: dataType, Data: changes}
	}
}

type saveRequestContract struct {
	Requests map[string]json.RawMessage `json:"requests"`
	Imps     map[string]json.RawMessage `json:"imps"`
	Accounts map[string]json.RawMessage `json:"accounts"`
}

type invalidationRequestContract struct {
	Requests []string `json:"requests"`
	Imps     []string `json:"imps"`
	Accounts []string `json:"accounts"`
}
