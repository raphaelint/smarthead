package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"remoteschool/smarthead/internal/platform/auth"
	"remoteschool/smarthead/internal/platform/datatable"
	"remoteschool/smarthead/internal/platform/web"
	"remoteschool/smarthead/internal/platform/web/webcontext"
	"remoteschool/smarthead/internal/platform/web/weberror"
	"remoteschool/smarthead/internal/student"

	"github.com/gorilla/schema"
	"github.com/pkg/errors"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/go-redis/redis"
)

// Students represents the Students API method handler set.
type Students struct {
	Repo     *student.Repository
	Redis    *redis.Client
	Renderer web.Renderer
}

func urlStudentsIndex() string {
	return fmt.Sprintf("/admin/students")
}

func urlStudentsView(subjectID string) string {
	return fmt.Sprintf("/admin/students/%s", subjectID)
}

func urlStudentsUpdate(subjectID string) string {
	return fmt.Sprintf("/admin/students/%s/update", subjectID)
}

// Index handles listing all the students for the current account.
func (h *Students) Index(ctx context.Context, w http.ResponseWriter, r *http.Request, params map[string]string) error {

	claims, err := auth.ClaimsFromContext(ctx)
	if err != nil {
		return err
	}

	fields := []datatable.DisplayField{
		{Field: "id", Title: "ID", Visible: false, Searchable: true, Orderable: true, Filterable: false},
		{Field: "name", Title: "Name", Visible: true, Searchable: true, Orderable: true, Filterable: true, FilterPlaceholder: "filter Name"},
		{Field: "username", Title: "Username", Visible: true, Searchable: true, Orderable: true, Filterable: true, FilterPlaceholder: "filter Username"},
		{Field: "age", Title: "Age", Visible: true, Searchable: true, Orderable: true},
		{Field: "current_class", Title: "Current Class", Visible: true, Searchable: true, Orderable: true, Filterable: true, FilterPlaceholder: "filter Class"},
		{Field: "parent_phone", Title: "Parent Phone", Visible: true, Searchable: true, Orderable: true, Filterable: true, FilterPlaceholder: "filter Phone"},
		{Field: "parent_email", Title: "Parent Email", Visible: true, Searchable: true, Orderable: true, Filterable: true, FilterPlaceholder: "filter Email"},
		{Field: "created_at", Title: "Registration Date", Visible: true, Searchable: true, Orderable: true, Filterable: true, FilterPlaceholder: "filter Date"},
	}

	mapFunc := func(q *student.Response, cols []datatable.DisplayField) (resp []datatable.ColumnValue, err error) {
		for i := 0; i < len(cols); i++ {
			col := cols[i]
			var v datatable.ColumnValue
			switch col.Field {
			case "id":
				v.Value = fmt.Sprintf("%s", q.ID)
			case "name":
				v.Value = q.Name
				v.Formatted = fmt.Sprintf("<a href='%s'>%s</a>", urlStudentsView(q.ID), v.Value)
			case "username":
				v.Value = q.Name
				v.Formatted = v.Value
			case "age":
				v.Value = fmt.Sprintf("%d", q.Age)
				v.Formatted = v.Value
			case "current_class":
				v.Value = fmt.Sprintf("%d", q.CurrentClass)
				v.Formatted = v.Value
			case "parent_phone":
				v.Value = q.ParentPhone
				v.Formatted = v.Value
			case "parent_email":
				v.Value = q.ParentEmail
				v.Formatted = v.Value
			case "created_at":
				v.Value = q.CreatedAt.LocalDate
				v.Formatted = v.Value
			default:
				return resp, errors.Errorf("Failed to map value for %s.", col.Field)
			}
			resp = append(resp, v)
		}

		return resp, nil
	}

	loadFunc := func(ctx context.Context, sorting string, fields []datatable.DisplayField) (resp [][]datatable.ColumnValue, err error) {
		res, err := h.Repo.Find(ctx, claims, student.FindRequest{
			Order: strings.Split(sorting, ","),
		})
		if err != nil {
			return resp, err
		}

		for _, a := range res {
			l, err := mapFunc(a.Response(ctx), fields)
			if err != nil {
				return resp, errors.Wrapf(err, "Failed to map student for display.")
			}

			resp = append(resp, l)
		}

		return resp, nil
	}

	dt, err := datatable.New(ctx, w, r, h.Redis, fields, loadFunc)
	if err != nil {
		return err
	}

	if dt.HasCache() {
		return nil
	}

	if ok, err := dt.Render(); ok {
		if err != nil {
			return err
		}
		return nil
	}

	data := map[string]interface{}{
		"datatable":         dt.Response(),
		"urlSubjectsCreate": urlSubjectsCreate(),
		"urlSubjectsIndex":  urlSubjectsIndex(),
	}

	return h.Renderer.Render(ctx, w, r, TmplLayoutBase, "admin-students-index.gohtml", web.MIMETextHTMLCharsetUTF8, http.StatusOK, data)
}

// View handles displaying a subjects.
func (h *Students) View(ctx context.Context, w http.ResponseWriter, r *http.Request, params map[string]string) error {

	studentID := params["student_id"]

	claims, err := auth.ClaimsFromContext(ctx)
	if err != nil {
		return err
	}

	data := make(map[string]interface{})
	f := func() (bool, error) {
		if r.Method == http.MethodPost {
			err := r.ParseForm()
			if err != nil {
				return false, err
			}

			switch r.PostForm.Get("action") {
			case "archive":
				err = h.Repo.Delete(ctx, claims, student.DeleteRequest{
					ID: studentID,
				})
				if err != nil {
					return false, err
				}

				webcontext.SessionFlashSuccess(ctx,
					"Student Archive",
					"Student successfully archive.")

				return true, web.Redirect(ctx, w, r, urlStudentsIndex(), http.StatusFound)
			}
		}

		return false, nil
	}

	end, err := f()
	if err != nil {
		return web.RenderError(ctx, w, r, err, h.Renderer, TmplLayoutBase, TmplContentErrorGeneric, web.MIMETextHTMLCharsetUTF8)
	} else if end {
		return nil
	}

	sub, err := h.Repo.ReadByID(ctx, claims, studentID)
	if err != nil {
		return err
	}
	data["student"] = sub.Response(ctx)
	data["urlStudentsIndex"] = urlStudentsIndex()
	data["urlStudentsView"] = urlStudentsView(studentID)
	data["urlStudentsUpdate"] = urlStudentsUpdate(studentID)

	return h.Renderer.Render(ctx, w, r, TmplLayoutBase, "admin-students-view.gohtml", web.MIMETextHTMLCharsetUTF8, http.StatusOK, data)
}

// Update handles updating a student.
func (h *Students) Update(ctx context.Context, w http.ResponseWriter, r *http.Request, params map[string]string) error {

	studentID := params["student_id"]

	claims, err := auth.ClaimsFromContext(ctx)
	if err != nil {
		return err
	}

	//
	req := new(student.UpdateRequest)
	data := make(map[string]interface{})
	f := func() (bool, error) {
		if r.Method == http.MethodPost {
			err := r.ParseForm()
			if err != nil {
				return false, err
			}

			decoder := schema.NewDecoder()
			decoder.IgnoreUnknownKeys(true)

			if err := decoder.Decode(req, r.PostForm); err != nil {
				return false, err
			}
			req.ID = studentID

			err = h.Repo.Update(ctx, claims, *req, time.Now())
			if err != nil {
				switch errors.Cause(err) {
				default:
					if verr, ok := weberror.NewValidationError(ctx, err); ok {
						data["validationErrors"] = verr.(*weberror.Error)
						return false, nil
					} else {
						return false, err
					}
				}
			}

			// Display a success message to the checklist.
			webcontext.SessionFlashSuccess(ctx,
				"student Updated",
				"Student successfully updated.")

			return true, web.Redirect(ctx, w, r, urlStudentsView(req.ID), http.StatusFound)
		}

		return false, nil
	}

	end, err := f()
	if err != nil {
		return web.RenderError(ctx, w, r, err, h.Renderer, TmplLayoutBase, TmplContentErrorGeneric, web.MIMETextHTMLCharsetUTF8)
	} else if end {
		return nil
	}

	sub, err := h.Repo.ReadByID(ctx, claims, studentID)
	if err != nil {
		return err
	}
	data["student"] = sub.Response(ctx)

	data["urlStudentsIndex"] = urlStudentsIndex()
	data["urlStudentsView"] = urlStudentsView(studentID)

	if req.ID == "" {
		req.Name = &sub.Name
	}
	data["form"] = req

	if verr, ok := weberror.NewValidationError(ctx, webcontext.Validator().Struct(student.UpdateRequest{})); ok {
		data["validationDefaults"] = verr.(*weberror.Error)
	}

	return h.Renderer.Render(ctx, w, r, TmplLayoutBase, "admin-students-update.gohtml", web.MIMETextHTMLCharsetUTF8, http.StatusOK, data)
}
