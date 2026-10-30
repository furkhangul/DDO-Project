package api

import (
	"net/http"

	"konakpro/internal/db"
	"konakpro/internal/platform/apperr"
)

var (
	errRouteNotFound    = apperr.New(http.StatusNotFound, "ROUTE_NOT_FOUND", "Uç nokta bulunamadı")
	errMethodNotAllowed = apperr.New(http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Bu yöntem desteklenmiyor")
)

const (
	reception    = db.UserRoleReception
	reservations = db.UserRoleReservations
	hkStaff      = db.UserRoleHousekeeping
	revenue      = db.UserRoleRevenue
	manager      = db.UserRoleManager
	guest        = db.UserRoleGuest
)

var (
	staff       = []db.UserRole{reception, reservations, hkStaff, revenue, manager}
	frontOffice = []db.UserRole{reception, reservations, manager}
	desk        = []db.UserRole{reception, manager}
	finance     = []db.UserRole{reception, revenue, manager}
	back        = []db.UserRole{revenue, manager}
	cleaning    = []db.UserRole{hkStaff, reception, manager}
	sales       = []db.UserRole{reception, reservations, revenue, manager}
	everyone    = []db.UserRole{reception, reservations, hkStaff, revenue, manager, guest}
	booker      = []db.UserRole{reception, reservations, manager, guest}
	ownable     = []db.UserRole{reception, reservations, revenue, manager, guest}
	admin       = []db.UserRole{manager}
)

type route struct {
	method     string
	path       string
	roles      []db.UserRole
	public     bool
	idempotent bool
	stream     bool
	handle     handler
}

const v1 = "/api/v1"

func (s *Server) routes() []route {
	return []route{
		{method: http.MethodPost, path: v1 + "/auth/login", public: true, handle: s.login},
		{method: http.MethodPost, path: v1 + "/auth/refresh", public: true, handle: s.refresh},
		{method: http.MethodPost, path: v1 + "/auth/logout", public: true, handle: s.logout},
		{method: http.MethodPost, path: v1 + "/channels/{code}/events", public: true, handle: s.channelEvent},

		{method: http.MethodGet, path: v1 + "/me", roles: everyone, handle: s.me},
		{method: http.MethodGet, path: v1 + "/live", roles: staff, stream: true, handle: s.handleLive},
		{method: http.MethodGet, path: v1 + "/dashboard", roles: staff, handle: s.dashboard},
		{method: http.MethodGet, path: v1 + "/users", roles: []db.UserRole{hkStaff, reception, manager}, handle: s.users},

		{method: http.MethodGet, path: v1 + "/room-types", roles: everyone, handle: s.listRoomTypes},
		{method: http.MethodPost, path: v1 + "/room-types", roles: admin, handle: s.createRoomType},
		{method: http.MethodPut, path: v1 + "/room-types/{id}", roles: admin, handle: s.updateRoomType},
		{method: http.MethodGet, path: v1 + "/rooms", roles: staff, handle: s.listRooms},
		{method: http.MethodPost, path: v1 + "/rooms", roles: admin, handle: s.createRoom},
		{method: http.MethodPut, path: v1 + "/rooms/{id}", roles: admin, handle: s.updateRoom},
		{method: http.MethodPost, path: v1 + "/rooms/adjacency", roles: admin, handle: s.addAdjacency},
		{method: http.MethodPut, path: v1 + "/rooms/{id}/status", roles: cleaning, handle: s.setRoomStatus},

		{method: http.MethodGet, path: v1 + "/availability", roles: everyone, handle: s.availability},
		{method: http.MethodGet, path: v1 + "/rate-plans", roles: everyone, handle: s.ratePlans},
		{method: http.MethodPost, path: v1 + "/quotes", roles: everyone, handle: s.quote},
		{method: http.MethodGet, path: v1 + "/calendar", roles: frontOffice, handle: s.calendar},
		{method: http.MethodGet, path: v1 + "/overbooking", roles: frontOffice, handle: s.overbooking},

		{method: http.MethodGet, path: v1 + "/reservations", roles: ownable, handle: s.listReservations},
		{method: http.MethodPost, path: v1 + "/reservations", roles: booker, idempotent: true, handle: s.createReservation},
		{method: http.MethodGet, path: v1 + "/reservations/{id}", roles: ownable, handle: s.getReservation},
		{method: http.MethodGet, path: v1 + "/reservations/{id}/price", roles: ownable, handle: s.reservationPrice},
		{method: http.MethodPost, path: v1 + "/reservations/{id}/modify", roles: frontOffice, idempotent: true, handle: s.modifyReservation},
		{method: http.MethodPost, path: v1 + "/reservations/{id}/cancel", roles: booker, idempotent: true, handle: s.cancelReservation},
		{method: http.MethodPost, path: v1 + "/reservations/{id}/walk", roles: desk, idempotent: true, handle: s.walkReservation},
		{method: http.MethodPost, path: v1 + "/reservations/{id}/check-in", roles: desk, idempotent: true, handle: s.checkIn},
		{method: http.MethodPost, path: v1 + "/reservations/{id}/self-check-in", roles: []db.UserRole{guest}, idempotent: true, handle: s.selfCheckIn},
		{method: http.MethodPost, path: v1 + "/reservations/{id}/check-out", roles: desk, idempotent: true, handle: s.checkOut},
		{method: http.MethodPost, path: v1 + "/reservations/{id}/move", roles: frontOffice, handle: s.moveReservation},
		{method: http.MethodPost, path: v1 + "/assignments/solve", roles: frontOffice, handle: s.solveAssignments},

		{method: http.MethodGet, path: v1 + "/reservations/{id}/folios", roles: ownable, handle: s.listFolios},
		{method: http.MethodGet, path: v1 + "/folios/{id}", roles: finance, handle: s.getFolio},
		{method: http.MethodPost, path: v1 + "/folios/{id}/charges", roles: finance, idempotent: true, handle: s.addCharge},
		{method: http.MethodPost, path: v1 + "/folios/{id}/payments", roles: finance, idempotent: true, handle: s.addPayment},
		{method: http.MethodPost, path: v1 + "/folios/{id}/refunds", roles: finance, idempotent: true, handle: s.addRefund},
		{method: http.MethodPost, path: v1 + "/folios/{id}/split", roles: finance, idempotent: true, handle: s.splitFolio},
		{method: http.MethodGet, path: v1 + "/folios/{id}/integrity", roles: finance, handle: s.folioIntegrity},

		{method: http.MethodGet, path: v1 + "/housekeeping/tasks", roles: cleaning, handle: s.listTasks},
		{method: http.MethodPost, path: v1 + "/housekeeping/tasks", roles: cleaning, handle: s.createTask},
		{method: http.MethodPut, path: v1 + "/housekeeping/tasks/{id}", roles: cleaning, handle: s.updateTask},

		{method: http.MethodGet, path: v1 + "/pricing", roles: sales, handle: s.pricingOverview},
		{method: http.MethodPut, path: v1 + "/pricing/settings", roles: back, handle: s.updatePricingSettings},
		{method: http.MethodPut, path: v1 + "/pricing/rates", roles: back, handle: s.upsertRate},
		{method: http.MethodPost, path: v1 + "/pricing/seasons", roles: back, handle: s.createSeason},
		{method: http.MethodDelete, path: v1 + "/pricing/seasons/{id}", roles: back, handle: s.deleteSeason},
		{method: http.MethodPost, path: v1 + "/pricing/early-tiers", roles: back, handle: s.upsertEarlyTier},
		{method: http.MethodDelete, path: v1 + "/pricing/early-tiers/{id}", roles: back, handle: s.deleteEarlyTier},
		{method: http.MethodPost, path: v1 + "/pricing/campaigns", roles: back, handle: s.createCampaign},
		{method: http.MethodPut, path: v1 + "/pricing/campaigns/{id}/active", roles: back, handle: s.setCampaignActive},
		{method: http.MethodPost, path: v1 + "/pricing/campaign-compat", roles: back, handle: s.addCompat},
		{method: http.MethodDelete, path: v1 + "/pricing/campaign-compat", roles: back, handle: s.removeCompat},
		{method: http.MethodGet, path: v1 + "/pricing/suggestions", roles: back, handle: s.priceSuggestions},

		{method: http.MethodGet, path: v1 + "/night-audit", roles: back, handle: s.auditStatus},
		{method: http.MethodPost, path: v1 + "/night-audit/run", roles: back, handle: s.runAudit},
		{method: http.MethodGet, path: v1 + "/reports/kpi", roles: back, handle: s.kpi},
		{method: http.MethodGet, path: v1 + "/audit-log", roles: admin, handle: s.auditLog},

		{method: http.MethodGet, path: v1 + "/channels", roles: sales, handle: s.listChannels},
		{method: http.MethodGet, path: v1 + "/channels/inbox", roles: sales, handle: s.listInbox},
		{method: http.MethodGet, path: v1 + "/channels/outbox", roles: sales, handle: s.listOutbox},
		{method: http.MethodPost, path: v1 + "/channels/outbox/{id}/retry", roles: back, handle: s.retryOutbox},
	}
}
