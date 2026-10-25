package main

import (
	"net/http"
	"strconv"

	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/auth"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/store"
)

func (app *application) usersHandler(w http.ResponseWriter, r *http.Request) {
	users, err := app.adminStore.ListUsers(r.Context())
	if err != nil {
		app.renderWithContext(w, r, "users", pageData{Title: "Users", Active: "users", Error: "Unable to load users."})
		return
	}
	app.renderWithContext(w, r, "users", pageData{
		Title:  "Users",
		Active: "users",
		Users:  users,
	})
}

func (app *application) userCreateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	actor, _ := auth.UserFromContext(r.Context())
	username := r.FormValue("username")
	email := r.FormValue("email")
	password := r.FormValue("password")
	isAdmin := r.FormValue("is_admin") == "true"

	if _, err := app.adminStore.CreateUser(r.Context(), actor.ID, username, email, password, isAdmin); err != nil {
		users, _ := app.adminStore.ListUsers(r.Context())
		app.renderWithContext(w, r, "users", pageData{
			Title:  "Users",
			Active: "users",
			Users:  users,
			Error:  "Unable to create user. Check that the username is unique and inputs are valid.",
		})
		return
	}
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

func (app *application) userToggleActiveHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	actor, _ := auth.UserFromContext(r.Context())
	targetID, err := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
	if err != nil || targetID < 1 {
		http.Error(w, "invalid user_id", http.StatusBadRequest)
		return
	}
	isActive := r.FormValue("is_active") == "true"
	if err := app.adminStore.SetUserActive(r.Context(), actor.ID, targetID, isActive); err != nil {
		http.Error(w, "unable to update user status", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

func (app *application) userResetPasswordHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	actor, _ := auth.UserFromContext(r.Context())
	targetID, err := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
	if err != nil || targetID < 1 {
		http.Error(w, "invalid user_id", http.StatusBadRequest)
		return
	}
	newPassword := r.FormValue("new_password")
	if err := app.adminStore.ResetUserPassword(r.Context(), actor.ID, targetID, newPassword); err != nil {
		users, _ := app.adminStore.ListUsers(r.Context())
		app.renderWithContext(w, r, "users", pageData{
			Title:  "Users",
			Active: "users",
			Users:  users,
			Error:  "Unable to reset password.",
		})
		return
	}
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

func (app *application) userToggleAdminHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	actor, _ := auth.UserFromContext(r.Context())
	targetID, err := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
	if err != nil || targetID < 1 {
		http.Error(w, "invalid user_id", http.StatusBadRequest)
		return
	}
	isAdmin := r.FormValue("is_admin") == "true"
	if err := app.adminStore.SetUserAdmin(r.Context(), actor.ID, targetID, isAdmin); err != nil {
		http.Error(w, "unable to update user role", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

func (app *application) groupsHandler(w http.ResponseWriter, r *http.Request) {
	groups, err := app.adminStore.ListGroups(r.Context())
	if err != nil {
		app.renderWithContext(w, r, "groups", pageData{Title: "Groups", Active: "groups", Error: "Unable to load groups."})
		return
	}
	users, _ := app.adminStore.ListUsers(r.Context())
	app.renderWithContext(w, r, "groups", pageData{
		Title:  "Groups",
		Active: "groups",
		Groups: groups,
		Users:  users,
	})
}

func (app *application) groupCreateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	actor, _ := auth.UserFromContext(r.Context())
	name := r.FormValue("name")
	desc := r.FormValue("description")
	if _, err := app.adminStore.CreateGroup(r.Context(), actor.ID, name, desc); err != nil {
		groups, _ := app.adminStore.ListGroups(r.Context())
		users, _ := app.adminStore.ListUsers(r.Context())
		app.renderWithContext(w, r, "groups", pageData{
			Title:  "Groups",
			Active: "groups",
			Groups: groups,
			Users:  users,
			Error:  "Unable to create group. Check that the group name is unique.",
		})
		return
	}
	http.Redirect(w, r, "/groups", http.StatusSeeOther)
}

func (app *application) groupUpdateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	actor, _ := auth.UserFromContext(r.Context())
	groupID, err := strconv.ParseInt(r.FormValue("group_id"), 10, 64)
	if err != nil || groupID < 1 {
		http.Error(w, "invalid group_id", http.StatusBadRequest)
		return
	}
	if err := app.adminStore.UpdateGroup(r.Context(), actor.ID, groupID, r.FormValue("name"), r.FormValue("description")); err != nil {
		http.Error(w, "unable to update group", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/groups", http.StatusSeeOther)
}

func (app *application) groupDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	actor, _ := auth.UserFromContext(r.Context())
	groupID, err := strconv.ParseInt(r.FormValue("group_id"), 10, 64)
	if err != nil || groupID < 1 {
		http.Error(w, "invalid group_id", http.StatusBadRequest)
		return
	}
	if err := app.adminStore.DeleteGroup(r.Context(), actor.ID, groupID); err != nil {
		http.Error(w, "unable to delete group", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/groups", http.StatusSeeOther)
}

func (app *application) groupAddMemberHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	actor, _ := auth.UserFromContext(r.Context())
	groupID, err1 := strconv.ParseInt(r.FormValue("group_id"), 10, 64)
	userID, err2 := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
	if err1 != nil || err2 != nil || groupID < 1 || userID < 1 {
		http.Error(w, "invalid group_id or user_id", http.StatusBadRequest)
		return
	}
	if err := app.adminStore.AddGroupMember(r.Context(), actor.ID, groupID, userID); err != nil {
		http.Error(w, "unable to add member to group", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/groups", http.StatusSeeOther)
}

func (app *application) groupRemoveMemberHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	actor, _ := auth.UserFromContext(r.Context())
	groupID, err1 := strconv.ParseInt(r.FormValue("group_id"), 10, 64)
	userID, err2 := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
	if err1 != nil || err2 != nil || groupID < 1 || userID < 1 {
		http.Error(w, "invalid group_id or user_id", http.StatusBadRequest)
		return
	}
	if err := app.adminStore.RemoveGroupMember(r.Context(), actor.ID, groupID, userID); err != nil {
		http.Error(w, "unable to remove member from group", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/groups", http.StatusSeeOther)
}

func (app *application) accessHandler(w http.ResponseWriter, r *http.Request) {
	grants, err := app.adminStore.ListDatabaseGrants(r.Context())
	if err != nil {
		app.renderWithContext(w, r, "access", pageData{Title: "Access management", Active: "access", Error: "Unable to load access grants."})
		return
	}
	groups, _ := app.adminStore.ListGroups(r.Context())
	conns, _ := store.ListDatabaseConnections(r.Context(), app.stores.postgres)
	app.renderWithContext(w, r, "access", pageData{
		Title:       "Access management",
		Active:      "access",
		Grants:      grants,
		Groups:      groups,
		Connections: conns,
	})
}

func (app *application) accessGrantHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	actor, _ := auth.UserFromContext(r.Context())
	groupID, err1 := strconv.ParseInt(r.FormValue("group_id"), 10, 64)
	dbID, err2 := strconv.ParseInt(r.FormValue("database_connection_id"), 10, 64)
	level := r.FormValue("access_level")
	if err1 != nil || err2 != nil || groupID < 1 || dbID < 1 {
		http.Error(w, "invalid group or database connection", http.StatusBadRequest)
		return
	}
	if err := app.adminStore.UpsertDatabaseGrant(r.Context(), actor.ID, groupID, dbID, level); err != nil {
		http.Error(w, "unable to grant database access", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/access", http.StatusSeeOther)
}

func (app *application) accessRevokeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	actor, _ := auth.UserFromContext(r.Context())
	groupID, err1 := strconv.ParseInt(r.FormValue("group_id"), 10, 64)
	dbID, err2 := strconv.ParseInt(r.FormValue("database_connection_id"), 10, 64)
	if err1 != nil || err2 != nil || groupID < 1 || dbID < 1 {
		http.Error(w, "invalid group or database connection", http.StatusBadRequest)
		return
	}
	if err := app.adminStore.RevokeDatabaseGrant(r.Context(), actor.ID, groupID, dbID); err != nil {
		http.Error(w, "unable to revoke database access", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/access", http.StatusSeeOther)
}

func (app *application) auditLogsHandler(w http.ResponseWriter, r *http.Request) {
	logs, err := app.adminStore.ListAuditLogs(r.Context(), 100)
	if err != nil {
		app.renderWithContext(w, r, "audit-logs", pageData{Title: "Audit logs", Active: "audit", Error: "Unable to load audit logs."})
		return
	}
	app.renderWithContext(w, r, "audit-logs", pageData{
		Title:     "Audit logs",
		Active:    "audit",
		AuditLogs: logs,
	})
}
