# Notes Server

This is a small HTTP server that handles personal notes.
It presents notes as a collection of markdown (commonmark) documents,
and allows to edit them directly in a web interface.

This tool is targeted for personal use only, and implements some rudimentary safety checks against accidental exposure to the public internet:
it only serves requests coming from the private network ranges.

This server keeps its data in a single SQLite database.

## Composing

At the root page it serves a list of all notes.

To create a new note/page, navigate to its to-be location, you'll see a 404 error page with a link.

This tool relies on the [Monaco editor] as its composer.
Editor covers the whole page, to submit your changes, use `Cmd-s` or `Ctrl-s` hotkey.

The very first line of the note becomes its title.

[Monaco editor]: https://microsoft.github.io/monaco-editor/

## Tags

Notes may have tags assigned for categorization.
Tags are read directly from the note body, from the first “HTML comment” section.

Tags must be a set of comma-separated terms located on a single line,
prefixed with the `Tags:` substring.

Some examples, that are equivalent:

```html
<!-- Tags: tag1, tag2 -->
```

```html
<!--
    Tags: tag1, tag2
-->
```

```html
<!--
    Some text.
    Tags: tag1, tag2
    More text.
-->
```

## Uploads

You can also attach files by dragging them into the editor.
Once file uploads, you'll get a relative link to it at the cursor position.
Note that currently files are stored directly in the same database as notes.
Current limit for a single file is 10MiB.

Some quirks:

* Files can only be uploaded to an already saved notes.
  Attempts to upload file to a new note that's not saved yet will fail.
* If you delete a note, all its attachments are deleted too.

## Search

For full text search this tool relies on [SQLite FTS5 extension],
see its [syntax] for more details.

[SQLite FTS5 extension]: http://sqlite.org/fts5.html
[syntax]: https://sqlite.org/fts5.html#full_text_query_syntax

## Backups

As this tool keeps all its data in a single database, backups are trivial.
There's a caveat that it's not safe to copy the database while the tool is running.
To safely get a backup file while the server is running, you can use the sqlite cli,
to open the database and issue a [VACUUM INTO] command.

Alternatively, you can use the tool under `tools/notes-backup` directory.
It creates a backup file, and uploads it to an S3 bucket.
It is intended to be run as a scheduled job.

[VACUUM INTO]: https://sqlite.org/lang_vacuum.html#vacuum_with_an_into_clause

## HTTPS

There's a special condition in the code that allows it to run as a HTTPS service.
It is intended for the split-DNS setups, where you keep a stub service running on the public HTTPS endpoint.

Such stub service relies on Let's Encrypt ACME API to get a certificate for a given domain name,
and store it into the same database that notes-server uses.

Notes server then can be run listening on the *private* HTTPS endpoint,
expecting requests for the same domain, and using certificate from the database.

There's an implementation of such a stub service under `tools/autocert-stub` directory.

## Contributions

This code is open source, but it is very opinionated and only implements the features that I personaly use.
It is likely not a good fit for any other use case that's different from that.

If you need some feature or change in the current behavior,
please fork this project and build your extension on top such fork.
It is unlikely that any pull requests with the functionality I'm not interested in will get merged into this repository.
