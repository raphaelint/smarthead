package deposit

import (
	"context"
	"net/http"
	"time"

	"remoteschool/smarthead/internal/platform/auth"
	"remoteschool/smarthead/internal/platform/web/webcontext"
	"remoteschool/smarthead/internal/postgres/models"

	"github.com/pborman/uuid"
	"github.com/pkg/errors"
	"github.com/rpip/paystack-go"
	"github.com/volatiletech/sqlboiler/boil"
	. "github.com/volatiletech/sqlboiler/queries/qm"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

var (
	// ErrNotFound abstracts the postgres not found error.
	ErrNotFound = errors.New("Entity not found")

	// ErrForbidden occurs when a user tries to do something that is forbidden to them according to our access control policies.
	ErrForbidden = errors.New("Attempted action is not allowed")
)

// Find gets all the subscription from the database based on the request params.
func (repo *Repository) Find(ctx context.Context, _ auth.Claims, req FindRequest) (Deposits, error) {
	var queries []QueryMod

	if req.Where != "" {
		queries = append(queries, Where(req.Where, req.Args...))
	}

	if len(req.Order) > 0 {
		for _, s := range req.Order {
			queries = append(queries, OrderBy(s))
		}
	}

	if req.Limit != nil {
		queries = append(queries, Limit(int(*req.Limit)))
	}

	if req.Offset != nil {
		queries = append(queries, Offset(int(*req.Offset)))
	}

	depositSlice, err := models.Deposits(queries...).All(ctx, repo.DbConn)
	if err != nil {
		return nil, err
	}

	var result Deposits
	for _, rec := range depositSlice {
		result = append(result, FromModel(rec))
	}

	return result, nil
}

// ReadByID gets the specified deposit by ID from the database.
func (repo *Repository) ReadByID(ctx context.Context, claims auth.Claims, id string) (*Deposit, error) {
	depositModel, err := models.FindDeposit(ctx, repo.DbConn, id)
	if err != nil {
		return nil, err
	}

	return FromModel(depositModel), nil
}

// Create inserts a new subscription into the database.
func (repo *Repository) Create(ctx context.Context, claims auth.Claims, req CreateRequest, now time.Time) (*Deposit, error) {
	span, ctx := tracer.StartSpanFromContext(ctx, "internal.deposit.Create")
	defer span.Finish()
	if claims.Audience == "" {
		return nil, errors.WithStack(ErrForbidden)
	}

	// Validate the request.
	v := webcontext.Validator()
	err := v.StructCtx(ctx, req)
	if err != nil {
		return nil, err
	}

	// If now empty set it to the current time.
	if now.IsZero() {
		now = time.Now()
	}

	// Always store the time as UTC.
	now = now.UTC()
	// Postgres truncates times to milliseconds when storing. We and do the same
	// here so the value we return is consistent with what we store.
	now = now.Truncate(time.Millisecond)
	m := models.Deposit{
		ID:        uuid.NewRandom().String(),
		StudentID: req.StudentID,
		CreatedAt: now,
		Amount:    req.Amount,
		Channel:   req.Channel,
		Status:    req.Status,
		Ref:       req.Ref,
	}

	if err := m.Insert(ctx, repo.DbConn, boil.Infer()); err != nil {
		return nil, errors.WithMessage(err, "Insert deposit failed")
	}

	return &Deposit{
		ID:        m.ID,
		StudentID: m.StudentID,
		CreatedAt: now,
		Amount:    m.Amount,
		Channel:   m.Channel,
		Status:    m.Status,
		Ref:       m.Ref,
	}, nil
}

// Update replaces an subject in the database.
func (repo *Repository) Update(ctx context.Context, claims auth.Claims, req UpdateRequest, now time.Time) error {
	span, ctx := tracer.StartSpanFromContext(ctx, "internal.deposit.Update")
	defer span.Finish()

	if claims.Audience == "" {
		return errors.WithStack(ErrForbidden)
	}

	// Validate the request.
	v := webcontext.Validator()
	err := v.StructCtx(ctx, req)
	if err != nil {
		return err
	}

	// If now empty set it to the current time.
	if now.IsZero() {
		now = time.Now()
	}

	// Always store the time as UTC.
	now = now.UTC()

	cols := models.M{}
	cols[models.DepositColumns.UpdatedAt] = now

	if req.Ref != nil {
		cols[models.DepositColumns.Ref] = *req.Ref
	}

	if req.Status != nil {
		cols[models.DepositColumns.Status] = *req.Status
	}

	if len(cols) == 0 {
		return nil
	}

	_,err = models.Deposits(models.DepositWhere.ID.EQ(req.ID)).UpdateAll(ctx, repo.DbConn, cols)

	return nil
}

// UpdateStatus updates the status of the the supplied deposit by quering the channel
func (repo *Repository) UpdateStatus(ctx context.Context, depositID string, now time.Time) error {
	depositModel, err := models.FindDeposit(ctx, repo.DbConn, depositID)
	if err != nil {
		return err
	}
	apiKey := "sk_test_b748a89ad84f35c2f1a8b81681f956274de048bb"
	client := paystack.NewClient(apiKey, http.DefaultClient)
	payment, err := client.Transaction.Verify(depositID)
	if err != nil {
		panic(err)
		// return err
	}
	if int(payment.Amount) * 100 < depositModel.Amount {
		return errors.Errorf("partial payment received. Expected %d, got %f", depositModel.Amount/100, payment.Amount)
	}
	depositModel.Status = StatusPaid
	_, err = depositModel.Update(ctx, repo.DbConn, boil.Infer())
	if err != nil {
		//Todo: log fatal error for admin to resolve
		return errors.New("payment received but unable to update status. contact admin for help")
	}
	return nil
}

// Delete removes an checklist from the database.
func (repo *Repository) Delete(ctx context.Context, claims auth.Claims, req DeleteRequest) error {
	span, ctx := tracer.StartSpanFromContext(ctx, "internal.deposit.Delete")
	defer span.Finish()

	// Validate the request.
	v := webcontext.Validator()
	err := v.Struct(req)
	if err != nil {
		return err
	}

	if claims.Audience == "" {
		return errors.WithStack(ErrForbidden)
	}
	// Admin users can update Categories they have access to.
	if !claims.HasRole(auth.RoleAdmin) {
		return errors.WithStack(ErrForbidden)
	}

	_, err = models.Deposits(models.DepositWhere.ID.EQ(req.ID)).DeleteAll(ctx, repo.DbConn)
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}
