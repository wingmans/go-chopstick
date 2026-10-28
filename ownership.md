Im working with edgar sec data. 

I want to track ownership of shares. Either the etf/wwakth managrrs or bog wiggs in the vomp like bezos or  ballmer. 

Which formtyoes dhould i use. 
Sketch the flow
Yes. If your goal is to build an SEC/EDGAR ownership tracker covering both large asset managers/ETFs and “big wigs” such as Jeff Bezos or Steve Ballmer, there are really three ownership streams to ingest.

                         SEC EDGAR
                            │
          ┌─────────────────┼─────────────────┐
          │                 │                 │
       FORM 13F         13D / 13G          3 / 4 / 5
          │                 │                 │
          ▼                 ▼                 ▼
   Institutional       Large beneficial      Insiders
     portfolios           owners            / executives
          │                 │                 │
   BlackRock etc.       >5% stakes       Bezos, CEO, CFO,
   hedge funds          activist/passive   directors, etc.
          │                 │                 │
          └─────────────────┼─────────────────┘
                            ▼
                    OWNERSHIP DATABASE
                            │
                  owner ↔ security ↔ date
                            │
                ┌───────────┴───────────┐
                ▼                       ▼
          Current position        Transactions /
             snapshots              changes
1. Form 13F — institutional managers
This is your main source for BlackRock, Vanguard, Berkshire, hedge funds, pension managers, investment advisers, etc. Institutional investment managers generally file 13F when they exercise investment discretion over at least $100 million in Section 13(f) securities. 

Think:

BlackRock ──13F──> AAPL   1.0B shares
                  MSFT   xxxM shares
                  NVDA   xxxM shares

Berkshire ─13F──> AAPL   xxxM shares
                  AXP    xxxM shares
Important: 13F is a quarterly portfolio snapshot, not a transaction feed. So derive:

Position(t) - Position(t-1) = inferred change
Don't interpret that as an exact purchase/sale date.

Also, if you're specifically trying to determine who owns an ETF, don't confuse the ETF's portfolio holdings with ownership of the ETF shares. A manager's 13F can report securities over which the manager has investment discretion; it isn't simply a list of the manager's own economic property. 

2. Schedule 13D / 13G — large shareholders
These are extremely valuable for your system.

They generally capture someone/group crossing more than 5% beneficial ownership of a covered class of equity. Both report shares, ownership percentage, and voting/investment power. 

The conceptual split is:

13G → qualifying large holders, often passive/institutional
13D → large holder where the fuller 13D disclosure regime applies
       (particularly interesting for control/activist situations)

13G/A, 13D/A → amendments / ownership changes
13D is especially rich because it can include the source of funds, purpose of the acquisition, plans regarding control of the issuer, and recent transactions. 

For an ownership graph, these filings give you strong edges:

PERSON / FUND
     │
     │ beneficial ownership
     ▼
   COMPANY
     │
     ├── shares
     ├── %
     ├── voting power
     └── investment power
3. Forms 3 / 4 / 5 — Bezos-style insiders
For executives, directors and other Section 16 reporting persons, these are essential.

Form 3 = initial ownership when someone becomes a reporting insider. Form 4 = subsequent changes in beneficial ownership and is normally filed very quickly—generally by the second business day after the transaction. Form 5 handles certain transactions that weren't required to be reported earlier or should have been reported but weren't. 

Form 4 should therefore be your transaction/event stream:

Bezos
  │
  ├─ Form 3 ─────> starting ownership
  │
  ├─ Form 4 ─────> sold 12M shares
  ├─ Form 4 ─────> sold 8M shares
  └─ Form 4 ─────> acquired/exercised/etc.
                         │
                         ▼
                  reconstruct position
Forms 3/4 also distinguish direct vs. indirect beneficial ownership, and the filing structure covers both non-derivative and derivative securities. 

How I'd structure your ingestion flow
EDGAR submissions / filings
          │
          ▼
    identify form
          │
 ┌────────┼───────────┐
 │        │           │
13F    13D/13G      3/4/5
 │        │           │
 ▼        ▼           ▼
Manager  Beneficial  Insider
snapshot ownership   events
 │        │           │
 └────────┴─────┬─────┘
                ▼
          NORMALIZATION
                │
        ┌───────┼────────┐
        ▼       ▼        ▼
      owner   issuer   security
      CIK      CIK      CUSIP
        │       │        │
        └───────┼────────┘
                ▼
         ownership_fact
                │
     ┌──────────┼──────────┐
     ▼          ▼          ▼
   shares       %       event/date
I would ingest 13F-HR, 13F-HR/A, SC 13D, SC 13D/A, SC 13G, SC 13G/A, 3, 3/A, 4, 4/A, 5, 5/A as the core set.

The key architectural point is not to collapse them into the same semantic event. A 13F row means roughly “manager reported this position as of quarter-end”; 13D/G means “reporting person reports beneficial ownership above the applicable threshold”; Form 4 means “this insider had this specific reportable transaction/change.” Treating those separately will prevent a lot of bad ownership-history data.

If you want, I can next sketch the actual database schema (owners, issuers, positions, transactions) and how I'd map the SEC XML fields from each form into it.

So in order to get a actual view i do not have to process years of data but the last full quarter and the current partial?
Yes—with an important distinction between establishing the latest known position and building its history.

For a “what is ownership now?” database, you generally do not need years of filings. You can bootstrap from the most recent complete snapshots and then process subsequent filings incrementally.

For example, suppose you initialize around today:

                   BOOTSTRAP
                      │
        ┌─────────────┼──────────────┐
        ▼             ▼              ▼
      13F-HR        13D/G          Forms 3/4/5
   latest quarter   latest active    recent/current
        │           ownership           │
        │               │               │
        ▼               ▼               ▼
  institutional     >5% owners       insiders
    snapshot          snapshot      transactions
        │               │               │
        └───────────────┼───────────────┘
                        ▼
               CURRENT OWNERSHIP
                        │
                        ▼
               ingest new filings
                 continuously
13F is easiest. Take each manager's latest 13F-HR (plus relevant amendment). That gives you their portfolio as of that report's quarter-end. You don't need earlier 13Fs unless you want historical changes.

13D/13G is trickier. I would not simply say “take last quarter.” These aren't quarterly portfolio snapshots in the same sense. A position may have originated years ago and still be relevant. You want the latest 13D/G state for each reporting owner/issuer relationship, including amendments. So you may need to look backward to discover still-active relationships—but you don't need to replay every filing once you've established the latest state.

Forms 3/4/5 are also different. If you want a Bezos-like person's current ownership, starting with an appropriate recent ownership disclosure and then applying subsequent Form 4 transactions can work. But blindly processing only the last quarter can miss a large pre-existing position.

So I'd design the pipeline around state + deltas, rather than “process N years”:

OWNER + SECURITY
       │
       ▼
┌──────────────────┐
│ BASELINE POSITION│  ← latest authoritative snapshot
└────────┬─────────┘
         │
         │ subsequent filings
         ▼
   + acquisitions
   - dispositions
   ± other changes
         │
         ▼
┌──────────────────┐
│ CURRENT ESTIMATE │
└──────────────────┘
This also means your initial backfill strategy can differ by form: 13F → latest filing per manager; 13D/G → find latest state of relevant ownership relationships; Forms 3/4/5 → establish a baseline per insider/security, then process later transactions.

If you're building this at EDGAR scale, that distinction can save you from unnecessarily parsing decades of Form 4s while still avoiding incorrect “current ownership” numbers.

Wxplain the distinction in more detail i dont get it yet
The key distinction is snapshot vs. event. That's what makes the SEC forms confusing.

Imagine you want to answer:

“How many Amazon shares does Bezos own right now?”

There are two fundamentally different ways a filing can give you information.

1. Snapshot: “I own X shares as of this date”
A snapshot gives you the whole position at a point in time.

                    TIME ─────────────────────────►

                       June 30
                          │
                          ▼
                    ┌───────────┐
                    │ 10M shares│
                    └───────────┘
                         SNAPSHOT

You now know:
June 30 position = 10M
You don't need to know whether the owner bought those shares in 1998, 2005, or 2024. The snapshot effectively resets your knowledge.

13F works largely like this. A manager reports its reportable 13F holdings as of quarter-end.

Suppose:

Vanguard → Amazon

2018   2019   2020   ...   Mar 2026   Jun 2026
 │      │      │              │           │
 ▼      ▼      ▼              ▼           ▼
???    ???    ???            710M        720M
                                      ↑
                              latest snapshot
If all you care about is the current/latest known position, you can ignore 2018–2025.

Your database can simply start:

owner       security    as_of       shares
Vanguard    AMZN        2026-06-30  720M
Then when the September 13F arrives, replace/update that state.

2. Event/delta: “I sold 2M shares”
Now consider a filing that only tells you about a change.

June 30
   ?
   │
   │ July 10: SOLD 2M
   ▼
 current = ???
You cannot calculate the new position because:

??? - 2M = ???
You need a baseline first.

If another disclosure tells you:

June 30: owns 10M       ← baseline
July 10: sells 2M       ← event
July 25: sells 1M       ← event

10M
 -2M
 -1M
────
 7M current estimate
That's why I used the phrase baseline + deltas.

Where Forms 3/4/5 fit
Insider reporting mixes these concepts.

Suppose Bezos has owned Amazon shares for decades. You start your database on July 1, 2026.

Then EDGAR gives you:

July 8    Form 4    Bezos sold 3M
July 15   Form 4    Bezos sold 2M
July 22   Form 4    Bezos sold 4M
If you only ingest the current quarter, you know:

Total sales = 9M

But...

Current ownership = ???
Because you don't know what he owned before those sales.

Fortunately, Form 4 is much more useful than a bare transaction log: its transaction rows generally include the amount beneficially owned following the reported transaction.

Conceptually:

FORM 4

Transaction:
    disposed:             3,000,000

Ownership after:
    shares owned:        912,000,000
                         ↑
                  THIS is extremely useful
That post-transaction ownership figure can give you a new baseline without reconstructing the person's entire history.

So you generally do not need to process every Bezos Form 4 back to the 1990s.

13D/G is different again
Suppose an investor bought 8% of XYZ in 2021.

2021
Investor crosses 5%
       │
       ▼
     13G
   owns 8%
       │
       │
       │
       │
       ▼
      2026
There might not be a brand-new “full portfolio snapshot” every quarter comparable to 13F.

If you say:

“I'll only download filings from Q2 and Q3 2026.”

you could fail to discover that ownership relationship in the first place.

That's why your bootstrap discovery may have to reach further back for 13D/G.

Once you find the relationship, however, you care primarily about its latest filing/amendment, not every historical version:

2021  13G       8.0%
       │
2022  13G/A     8.7%
       │
2023  13G/A     7.9%
       │
2024  13G/A     6.2%
       │
2025  13G/A     5.8%
       │
2026  13G/A     5.4%
                  ↑
            state you want
You don't need all six records to answer “what is the latest reported ownership?”

You need the last relevant state. The older records matter if you want a chart/history.

So think of your system as three pipelines
                         WHAT DO THEY OWN?
                                │
             ┌──────────────────┼──────────────────┐
             │                  │                  │
             ▼                  ▼                  ▼
            13F               13D/G              3/4/5
             │                  │                  │
        INSTITUTIONS        >5% OWNERS          INSIDERS
             │                  │                  │
             ▼                  ▼                  ▼
       Latest quarterly     Find latest       Latest reported
          snapshot          ownership state    post-transaction
             │                  │               ownership
             └──────────────────┼──────────────────┘
                                ▼
                         CURRENT-STATE TABLE

                owner │ company │ shares │ % │ as_of
Therefore, “last full quarter + current partial quarter” is a good ingestion strategy for 13F, but it is not a universal bootstrap rule for all ownership forms.

For 13D/G and insiders, the better rule is:

Find one sufficiently recent authoritative ownership state for each owner/security relationship. Then process everything forward from that point.

Once you've bootstrapped those states, the system becomes much simpler: every day you process only new EDGAR filings and update the affected ownership records.

How timely is insider reporting?
Very timely. Form 4 is one of the best SEC datasets for near-real-time ownership tracking.

For most reportable insider transactions, Form 4 is due by the end of the second business day after the transaction is executed. 
 So typically:

Monday     Bezos sells shares
Tuesday    business day 1
Wednesday  business day 2 → Form 4 deadline

                         ↓
                    EDGAR filing
                         ↓
              Your ingestion pipeline
This is dramatically fresher than 13F:

Source	What you learn	Typical lag
Form 4	Insider transaction + resulting holdings	≤ 2 business days
Form 3	Initial insider holdings	≤ 10 calendar days after becoming insider
Form 5	Certain deferred/missed reports	Up to 45 days after fiscal year-end
13F	Institutional portfolio snapshot	Much slower / quarterly
Form 4 is especially useful because it contains both the transaction and generally the amount beneficially owned following the transaction. So you can store something like:

transaction_date:  2026-09-14
filing_date:       2026-09-16

owner:             Jeff Bezos
issuer:            Amazon
transaction:       SALE
shares_changed:    -5,000,000

shares_after:      850,000,000
                         ↑
                    new baseline
That last field is crucial: you don't necessarily have to reconstruct decades of transactions to know the latest reported position.

There are exceptions. For example, certain Rule 10b5-1 transactions have special rules for determining the execution date when the insider doesn't immediately know the transaction occurred, so the effective delay can be longer. 
 And Form 5 catches some transactions eligible for deferred reporting or ones that should have been reported earlier. 

So for the system you're describing, I'd treat Form 4 as an event stream and ingest it continuously. You could get a surprisingly good insider-ownership product from EDGAR alone: “Bezos sold X yesterday; latest reported beneficial ownership = Y”, usually within a couple of business days.