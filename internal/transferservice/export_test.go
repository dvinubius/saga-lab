package transferservice

var (
	FundsDebited  = (*Service).fundsDebited
	DebitRejected = (*Service).debitRejected
	FundsCredited = (*Service).fundsCredited

	ProcessingObserved = (*Service).processingObserved
)
