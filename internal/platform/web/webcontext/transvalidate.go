package webcontext

import (
	"context"
	"reflect"
	"strings"

	"github.com/go-playground/locales/en"
	"github.com/go-playground/locales/fr"
	"github.com/go-playground/locales/id"
	"github.com/go-playground/locales/ja"
	"github.com/go-playground/locales/nl"
	"github.com/go-playground/locales/zh"
	ut "github.com/go-playground/universal-translator"
	"gopkg.in/go-playground/validator.v9"
	en_translations "gopkg.in/go-playground/validator.v9/translations/en"
	fr_translations "gopkg.in/go-playground/validator.v9/translations/fr"
	id_translations "gopkg.in/go-playground/validator.v9/translations/id"
	ja_translations "gopkg.in/go-playground/validator.v9/translations/ja"
	nl_translations "gopkg.in/go-playground/validator.v9/translations/nl"
	zh_translations "gopkg.in/go-playground/validator.v9/translations/zh"
)

// ctxKeyTranslate represents the type of value for the context key.
type ctxKeyTranslate int

// KeyTranslate is used to store/retrieve a Claims value from a context.Context.
const KeyTranslate ctxKeyTranslate = 1

// ContextWithTranslator appends a universal translator to a context.
func ContextWithTranslator(ctx context.Context, translator ut.Translator) context.Context {
	return context.WithValue(ctx, KeyTranslate, translator)
}

// ContextTranslator returns the universal context from a context.
func ContextTranslator(ctx context.Context) ut.Translator {
	if t, ok := ctx.Value(KeyTranslate).(ut.Translator); ok {
		return t
	}
	return uniTrans.GetFallback()
}

// validate holds the settings and caches for validating request struct values.
var validate *validator.Validate

// translator is a cache of locale and translation information.
var uniTrans *ut.UniversalTranslator

func init() {

	// Example from https://github.com/go-playground/universal-translator/issues/7

	// Instantiate the english and french locale for the validator library.
	en := en.New()
	fr := fr.New()
	id := id.New()
	ja := ja.New()
	nl := nl.New()
	zh := zh.New()

	// Create a value using English as the fallback locale (first argument).
	// Provide one or more arguments for additional supported locales.
	uniTrans = ut.New(en, en, fr, id, ja, nl, zh)

	// this is usually know or extracted from http 'Accept-Language' header
	// also see uni.FindTranslator(...)
	transEn, _ := uniTrans.GetTranslator(en.Locale())
	transFr, _ := uniTrans.GetTranslator(fr.Locale())
	transId, _ := uniTrans.GetTranslator(id.Locale())
	transJa, _ := uniTrans.GetTranslator(ja.Locale())
	transNl, _ := uniTrans.GetTranslator(nl.Locale())
	transZh, _ := uniTrans.GetTranslator(zh.Locale())

	transEn.Add("{{name}}", "Name", false)
	transFr.Add("{{name}}", "Nom", false)

	transEn.Add("{{first_name}}", "First Name", false)
	transFr.Add("{{first_name}}", "Prénom", false)

	transEn.Add("{{last_name}}", "Last Name", false)
	transFr.Add("{{last_name}}", "Nom de famille", false)

	validate = newValidator()

	en_translations.RegisterDefaultTranslations(validate, transEn)
	fr_translations.RegisterDefaultTranslations(validate, transFr)
	id_translations.RegisterDefaultTranslations(validate, transId)
	ja_translations.RegisterDefaultTranslations(validate, transJa)
	nl_translations.RegisterDefaultTranslations(validate, transNl)
	zh_translations.RegisterDefaultTranslations(validate, transZh)

	/*
		validate.RegisterTranslation("required", transEn, func(ut ut.Translator) error {
			return ut.Add("required", "{0} must have a value!", true) // see universal-translator for details
		}, func(ut ut.Translator, fe validator.FieldError) string {
			t, _ := ut.T("required", fe.Field())

			return t
		})

		validate.RegisterTranslation("required", transFr, func(ut ut.Translator) error {
			return ut.Add("required", "{0} must have a value!", true) // see universal-translator for details
		}, func(ut ut.Translator, fe validator.FieldError) string {
			t, _ := ut.T("required", fe.Field())

			return t
		})


		validate.RegisterTranslation("unique", transEn, func(ut ut.Translator) error {
			return ut.Add("unique", "{0} must be unique", true) // see universal-translator for details
		}, func(ut ut.Translator, fe validator.FieldError) string {
			t, _ := ut.T("unique", fe.Field())

			return t
		})

		validate.RegisterTranslation("unique", transFr, func(ut ut.Translator) error {
			return ut.Add("unique", "{0} must be unique", true) // see universal-translator for details
		}, func(ut ut.Translator, fe validator.FieldError) string {
			t, _ := ut.T("unique", fe.Field())

			return t
		})*/

}

type ctxKeyTagUnique int

const KeyTagUnique ctxKeyTagUnique = 1

// newValidator inits a new validator with custom settings.
func newValidator() *validator.Validate {
	var v = validator.New()

	// Use JSON tag names for errors instead of Go struct names.
	v.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]

		if name == "-" {
			return ""
		}

		return "{{" + name + "}}"
	})

	// Empty method that can be overwritten in business logic packages to prevent web.Decode from failing.
	f := func(fl validator.FieldLevel) bool {
		return false
	}
	v.RegisterValidation("unique", f)

	fctx := func(ctx context.Context, fl validator.FieldLevel) bool {
		if fl.Field().String() == "invalid" {
			return false
		}
		return ctx.Value(KeyTagUnique).(bool)
	}
	v.RegisterValidationCtx("unique", fctx)

	return v
}

// Validator returns the current init validator.
func Validator() *validator.Validate {
	return validate
}

// UniversalTranslator returns the current UniversalTranslator.
func UniversalTranslator() *ut.UniversalTranslator {
	return uniTrans
}
