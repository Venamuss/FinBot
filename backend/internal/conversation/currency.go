package conversation

import (
	"regexp"
	"strings"
	"unicode"
)

// ISO 4217 List One (including fund/metal/testing codes), published 2026-09-17.
// https://www.six-group.com/dam/download/financial-information/data-center/iso-currrency/lists/list-one.xml
// This is a rejection lookup, not conversion or multi-currency support.
const currencyCodes = " " +
	"AED AFN ALL AMD AOA ARS AUD AWG AZN BAM BBD BDT BHD BIF BMD BND BOB BOV BRL BSD " +
	"BTN BWP BYN BZD CAD CDF CHE CHF CHW CLF CLP CNY COP COU CRC CUP CVE CZK DJF DKK " +
	"DOP DZD EGP ERN ETB EUR FJD FKP GBP GEL GHS GIP GMD GNF GTQ GYD HKD HNL HTG HUF " +
	"IDR ILS INR IQD IRR ISK JMD JOD JPY KES KGS KHR KMF KPW KRW KWD KYD KZT LAK LBP " +
	"LKR LRD LSL LYD MAD MDL MGA MKD MMK MNT MOP MRU MUR MVR MWK MXN MXV MYR MZN NAD " +
	"NGN NIO NOK NPR NZD OMR PAB PEN PGK PHP PKR PLN PYG QAR RON RSD RUB RWF SAR SBD " +
	"SCR SDG SEK SGD SHP SLE SOS SRD SSP STN SVC SYP SZL THB TJS TMT TND TOP TRY TTD " +
	"TWD TZS UAH UGX USD USN UYI UYU UYW UZS VED VES VND VUV WST XAD XAF XAG XAU XBA " +
	"XBB XBC XBD XCD XCG XDR XOF XPD XPF XPT XSU XTS XUA XXX YER ZAR ZMW ZWG "

var currencyWords = regexp.MustCompile(`\p{L}+`)
var amountBeforeCurrency = regexp.MustCompile(`[0-9]+(?:[.,][0-9]+)?\s*$`)
var amountAfterCurrency = regexp.MustCompile(`^\s*[0-9]+(?:[.,][0-9]+)?`)

// These guards validate currency only. Intent, amounts, dates and quantities
// remain the model's responsibility; no expense command parser runs here.
func unsupportedCurrency(text string) bool {
	for _, character := range text {
		if unicode.Is(unicode.Sc, character) && character != '₽' {
			return true
		}
	}
	if unsupportedCurrencyCode(text) {
		return true
	}

	for _, word := range strings.FieldsFunc(strings.ToLower(text), nonLetter) {
		switch word {
		case "доллар", "доллара", "долларов", "доллары", "долларах", "евро",
			"юань", "юаня", "юаней", "юани", "юанях", "иен", "иена", "иены", "йен", "йена", "йены",
			"фунт", "фунта", "фунтов", "фунты", "фунтах", "гривна", "гривны", "гривен", "тенге",
			"дирхам", "дирхама", "дирхамов", "лира", "лиры", "лир", "рупия", "рупии", "рупий",
			"франк", "франка", "франков", "биткоин", "биткоина", "биткоинов":
			return true
		}
	}
	return false
}

// Codes must touch a numeric monetary token, with optional whitespace. Product
// words elsewhere (USB кабель 250, SSD 500GB 4000) are not currency evidence.
func unsupportedCurrencyCode(text string) bool {
	for _, position := range currencyWords.FindAllStringIndex(text, -1) {
		word := text[position[0]:position[1]]
		code := strings.ToUpper(word)
		if code == "RUB" || code == "RUR" {
			continue
		}

		before := text[:position[0]]
		after := text[position[1]:]
		preceding := amountBeforeCurrency.FindStringIndex(before)
		following := amountAfterCurrency.FindStringIndex(after)
		hasAmountBefore := preceding != nil && expressionBoundary(before, preceding[0], preceding[1])
		hasAmountAfter := following != nil && expressionBoundary(after, following[0], following[1])
		if !hasAmountBefore && !hasAmountAfter {
			continue
		}

		if strings.Contains(currencyCodes, " "+code+" ") || code == "USDT" || code == "BTC" || code == "ETH" {
			return true
		}

		// An unknown uppercase three-letter suffix at the end of a price is
		// ambiguous. Ask explicitly rather than assume that it means RUB.
		if hasAmountBefore && asciiCurrencySuffix(word) && strings.Trim(after, " \t\r\n.,!?;:") == "" {
			return true
		}
	}
	return false
}

func asciiCurrencySuffix(word string) bool {
	if len(word) != 3 {
		return false
	}
	for _, character := range word {
		if character < 'A' || character > 'Z' {
			return false
		}
	}
	return true
}

func explicitRubles(text string) bool {
	if strings.ContainsRune(text, '₽') {
		return true
	}
	for _, word := range strings.FieldsFunc(strings.ToLower(text), nonLetter) {
		switch word {
		case "rub", "rur", "р", "руб", "рубль", "рубля", "рублей", "рубли", "рублях":
			return true
		}
	}
	return false
}

func nonLetter(character rune) bool {
	return !unicode.IsLetter(character)
}

func currencyClarification(operation action) action {
	result := copyAction(operation)
	field := "currency"
	result.args["field"] = &field
	if operation.name == "create_expense" || operation.name == "update_expense" {
		intent := operation.name
		result.args["intent"] = &intent
	}
	result.name = "clarify"
	return result
}
