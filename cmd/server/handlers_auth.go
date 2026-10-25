package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/auth"
)

func (app *application) loginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if rawToken := app.sessions.ReadCookie(r); rawToken != "" {
			if sess, _, err := app.sessions.ValidateAndTouch(r.Context(), rawToken); err == nil && sess != nil {
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
		}
		render(w, "login", pageData{Title: "Sign in", Active: "login"})
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		render(w, "login", pageData{Title: "Sign in", Active: "login", Error: "Invalid form submission."})
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")

	user, sess, err := app.authSvc.Authenticate(r.Context(), username, password)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		render(w, "login", pageData{
			Title:  "Sign in",
			Active: "login",
			Error:  auth.ErrInvalidCredentials.Error(),
		})
		return
	}

	_ = app.adminStore.RecordAuditLog(r.Context(), &user.ID, "auth.login", "session", fmt.Sprintf("%d", sess.ID), nil, map[string]any{
		"username": user.Username,
	})

	app.sessions.SetCookie(w, sess.RawToken)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (app *application) signupHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if rawToken := app.sessions.ReadCookie(r); rawToken != "" {
			if sess, _, err := app.sessions.ValidateAndTouch(r.Context(), rawToken); err == nil && sess != nil {
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
		}
		render(w, "signup", pageData{Title: "Sign up", Active: "signup"})
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		render(w, "signup", pageData{Title: "Sign up", Active: "signup", Error: "Invalid form submission."})
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	confirmPassword := r.FormValue("confirm_password")

	if password != confirmPassword {
		w.WriteHeader(http.StatusBadRequest)
		render(w, "signup", pageData{
			Title:  "Sign up",
			Active: "signup",
			Error:  "Password and confirmation password do not match.",
		})
		return
	}

	user, err := app.authSvc.RegisterUser(r.Context(), username, email, password)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		render(w, "signup", pageData{
			Title:  "Sign up",
			Active: "signup",
			Error:  "Unable to register account. Check that username/email is unique and password is at least 8 characters.",
		})
		return
	}

	_ = app.adminStore.RecordAuditLog(r.Context(), &user.ID, "auth.signup", "user", fmt.Sprintf("%d", user.ID), nil, map[string]any{
		"username": user.Username,
	})

	_, sess, err := app.authSvc.Authenticate(r.Context(), username, password)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	app.sessions.SetCookie(w, sess.RawToken)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (app *application) logoutHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rawToken := app.sessions.ReadCookie(r)
	if u, ok := auth.UserFromContext(r.Context()); ok {
		_ = app.adminStore.RecordAuditLog(r.Context(), &u.ID, "auth.logout", "session", "", nil, nil)
	}
	_ = app.sessions.Revoke(r.Context(), rawToken)
	app.sessions.ClearCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
