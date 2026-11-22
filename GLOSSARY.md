# GoRaffle

A shared-screen raffle: a host keeps a List of Entries and runs repeated Draws that every connected browser watches together. The Draws are fair: nobody is picked again until everyone eligible has been picked as often.

## Language

### The list

**List**:
The named set of Entries a raffle draws from.
_Avoid_: Roster, pool (pool means the Eligible pool)

**Entry**:
One name on the List that can be picked: a person, team, or anything else being raffled.
_Avoid_: Contestant, participant, candidate

**Tag**:
A label attached to Entries so a Draw can be limited to some of them.
_Avoid_: Category, group

**Excluded**:
An Entry the host has taken out of every Draw. It stays on the List, keeps its Pick count, and can be brought back.
_Avoid_: Disabled, inactive, hidden

**List file**:
A JSON file holding a List's name, its Entries and its History: the one format a List is exported as and restored from.
_Avoid_: Backup, export file, CSV (CSV is a legacy import format without History)

**Import**:
Replacing the List's name, Entries and History with the contents of a List file. It never merges with what was there.
_Avoid_: Merge, upload, restore

### Drawing

**Tag filter**:
The Tags that limit a Draw. **Must-have** Tags are all required; of the **any-of** Tags an Entry needs at least one. An empty Tag filter limits nothing.
_Avoid_: Filter, query

**Eligible pool**:
The Entries a Draw considers: everything on the List that is not Excluded and passes the Tag filter.
_Avoid_: Candidates, participants

**Pot**:
The Entries in the Eligible pool with the fewest Picks: the only ones who can win the next Draw.
_Avoid_: Winnable tier, min-count pool, shortlist

**Draw**:
One act of choosing a winner at random from the Pot and recording it as a Pick.
_Avoid_: Spin, roll, raffle (for a single act)

**Pick**:
The record that an Entry won a Draw. It carries a Pick number.
_Avoid_: Win, result, selection

**Pick count**:
How many Picks an Entry has. It decides who is in the Pot.
_Avoid_: Score, wins, times picked

**Pick number**:
The position of a Pick in the History: 1 for the first Pick since the last Reset, and one higher for each after it.
_Avoid_: Draw id, round number

**Season**:
The stretch in which every Entry in the Eligible pool gets picked one more time. As it nears its end the Pot shrinks to the last few stragglers, and everyone knows they will be picked.
_Avoid_: Round, cycle

**History**:
The ordered list of Picks since the last Reset.
_Avoid_: Log, results

**Undo**:
Taking back the most recent Pick, which also lowers that Entry's Pick count.
_Avoid_: Revert, rollback

**Reset**:
Clearing all Picks and setting every Pick count back to zero. The Entries and Tags stay.
_Avoid_: Clear (Clear removes the Entries themselves), new season

### The reveal

**Reveal**:
What every browser is shown together for one Draw: the Suspense, then the winner.
_Avoid_: Announcement, result screen

**Suspense**:
The moment before the winner is shown, when names from the Pot flash past on the Reel.
_Avoid_: Drama, animation

**Reel**:
The slot-machine strip that names scroll across during the Suspense and that stops on the winner. It runs horizontally or vertically.
_Avoid_: Wheel, spinner, drum

**Reel speed**:
How many names pass the Reel's window per second. It is the same whichever way the Reel runs, unless the names are so wide that the Reel would move too fast to read.
_Avoid_: Spin speed, velocity (for the pixel rate the Reel moves at)
