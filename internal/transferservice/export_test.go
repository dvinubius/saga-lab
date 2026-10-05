package transferservice

var (
	FundsDebited   = (*Service).fundsDebited
	DebitRejected  = (*Service).debitRejected
	FundsCredited  = (*Service).fundsCredited
	CreditRejected = (*Service).creditRejected

	ProcessingObserved = (*Service).processingObserved
)
