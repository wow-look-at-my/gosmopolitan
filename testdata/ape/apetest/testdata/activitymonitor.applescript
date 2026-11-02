-- Opens Activity Monitor and prints each text in its window that contains
-- one of the words given as arguments, one text per line. When nothing
-- matches, it prints how many elements of each kind the window held.
on run argv
	tell application "Activity Monitor" to activate
	-- Activity Monitor samples every few seconds, so a new process shows up late.
	delay 8
	set found to {}
	set kinds to {}
	tell application "System Events"
		tell process "Activity Monitor"
			-- The search field keeps only the processes whose name holds the first word,
			-- so the scan below reads a few rows rather than the whole table.
			keystroke "f" using command down
			keystroke (item 1 of argv)
			delay 4
			set elems to entire contents of front window
			repeat with elem in elems
				try
					set end of kinds to (class of elem) as text
				end try
				-- A table cell can carry its text as its value, its name or its description.
				repeat with field in {"value", "name", "description"}
					try
						if field as text is "value" then
							set shown to (value of elem) as text
						else if field as text is "name" then
							set shown to (name of elem) as text
						else
							set shown to (description of elem) as text
						end if
						repeat with wanted in argv
							if shown contains (wanted as text) and found does not contain shown then set end of found to shown
						end repeat
					end try
				end repeat
			end repeat
		end tell
	end tell
	tell application "Activity Monitor" to quit
	set AppleScript's text item delimiters to linefeed
	if found is {} then return "no match among " & (count of elems) & " elements: " & (my summary(kinds))
	return found as text
end run

-- summary counts each distinct kind in kinds.
on summary(kinds)
	set seen to {}
	set out to ""
	repeat with kind in kinds
		set kind to kind as text
		if seen does not contain kind then
			set end of seen to kind
			set total to 0
			repeat with other in kinds
				if (other as text) is kind then set total to total + 1
			end repeat
			set out to out & kind & "=" & total & " "
		end if
	end repeat
	return out
end summary
