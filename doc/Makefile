# Create manpages. If installed for a particular user, these can be used
# with `zelta help` by setting the path via ZELTA_DOC, e.g.,
# `export ZELTA_DOC="${HOME}/.local/share/zelta/doc"`

SRC      = $(wildcard zelta*.md) zprune.md
MAN7     = $(patsubst %.7.md,man7/%.7,$(filter %.7.md,$(SRC)))
MAN8     = $(patsubst %.md,man8/%.8,$(filter-out %.7.md,$(SRC)))
ALIASES  = man8/zelta-sync.8

all: $(MAN7) $(MAN8) $(ALIASES)

# Specific rule for Section 7 (Misc/Overview)
man7/%.7: %.7.md
	mkdir -p man7
	pandoc --standalone --to man $< -o $@

# Catch-all for Section 8 (Admin commands)
man8/%.8: %.md
	mkdir -p man8
	pandoc --standalone --to man $< -o $@

man8/zelta-sync.8: man8/zelta-backup.8
	ln -sf zelta-backup.8 $@

clean:
	rm -f $(MAN7) $(MAN8) $(ALIASES)

.PHONY: all clean
