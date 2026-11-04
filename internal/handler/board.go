package handler

import (
	"fmt"
	"net/http"

	db "github.com/vsriram/simple-host/internal/db"
)

// Saved data, step 4: Shared board (kind "board"; saved-data redesign, owner
// approved 2026-09-27). A list a group keeps together: a shopping list, a
// kanban, a sign-up grid.
//
//   - Anyone who can open the site reads it (who added each item stays the
//     owner's to see), as a public list.
//   - Signed-in visitors allowed to save (the site's who-may-save setting)
//     add items (POST .../data/{name}), and change (PATCH) and delete (DELETE
//     .../data/{name}/items/{id}) any item, one at a time. A change may name
//     the item's version (If-Match); a stale one is refused with 409
//     version_conflict and the current item, so two people never overwrite
//     each other unseen. Whoever deleted an item can bring it back for
//     SAVED_DATA_WITHDRAW_UNDO_MINUTES; the owner restores anything from
//     History and Recently deleted for SAVED_DATA_UNDO_DAYS.
//   - Only the owner empties or replaces the whole board (the list's clear,
//     with its typed confirmation); nobody else has a route that touches more
//     than one item.
//   - Items are JSON objects of at most SAVED_DATA_BOARD_ITEM_MAX_KB, at most
//     SAVED_DATA_BOARD_MAX live per name (the owner's restores too); visitor
//     adds, changes and deletes share the per-address write rate
//     (SAVED_DATA_APPEND_PER_MIN) and, per signed-in person whatever their
//     address, SAVED_DATA_BOARD_WRITES_PER_MIN.
//   - The owner's "Restore all" on a board names a window (within_minutes):
//     after vandalism it brings back what went since then, not what people
//     deleted on purpose before.
//   - No live feed (decided later): a page polls GET with If-None-Match and
//     gets 304 while nothing changed.

func (h *SiteHandler) boardItemMax() int64 { return int64(h.savedData.BoardItemMaxKB) << 10 }

func (h *SiteHandler) writeBoardFull(w http.ResponseWriter) {
	writeJSON(w, http.StatusConflict, errorResponse{
		Error: fmt.Sprintf("this board is full (%d items); delete some to make room", h.savedData.BoardMax),
		Code:  "list_full",
	})
}

// boardItemOut is an item as a board answers it: its version, and never who
// added it (that is the owner's, in the owner's reads).
func boardItemOut(it db.CollectionItem) db.CollectionItem {
	it.Data = withoutSubmitter(it.Data)
	it.By = ""
	return it
}
