/*
 * Copyright (c) 2016-2021 Chuck Tuffli <chuck@tuffli.net>
 *
 * Permission to use, copy, modify, and distribute this software for any
 * purpose with or without fee is hereby granted, provided that the above
 * copyright notice and this permission notice appear in all copies.
 *
 * THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
 * WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
 * MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
 * ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
 * WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
 * ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
 * OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
 */

//go:build freebsd

#include <stdio.h>
#include <stdlib.h>
#include <stdbool.h>
#include <stddef.h>
#include <assert.h>
#include <err.h>
#include <string.h>
#include <sys/endian.h>

#ifdef LIBXO
#include <libxo/xo.h>
#endif

#include "libsmart.h"
#include "libsmart_priv.h"
#include "libsmart_dev.h"

/* Default page lists */

smart_page_list_t pg_list_ata = {
	.pg_count = 3,
	.pages = {
		{ .id = PAGE_ID_ATA_SMART_READ_DATA, .bytes = 512 },
		{ .id = PAGE_ID_ATA_SMART_READ_THRESHOLDS, .bytes = 512 },
		{ .id = PAGE_ID_ATA_SMART_RET_STATUS, .bytes = 4 }
	}
};

#define PAGE_ID_NVME_SMART_HEALTH	0x02

smart_page_list_t pg_list_nvme = {
	.pg_count = 1,
	.pages = {
		{ .id = PAGE_ID_NVME_SMART_HEALTH, .bytes = 512 }
	}
};

smart_page_list_t pg_list_scsi = {
	.pg_count = 12,
	.pages = {
		{ .id = PAGE_ID_SCSI_WRITE_ERR, .bytes = 128 },
		{ .id = PAGE_ID_SCSI_READ_ERR, .bytes = 128 },
		{ .id = PAGE_ID_SCSI_VERIFY_ERR, .bytes = 128 },
		{ .id = PAGE_ID_SCSI_NON_MEDIUM_ERR, .bytes = 128 },
		{ .id = PAGE_ID_SCSI_LAST_N_ERR, .bytes = 512 },
		{ .id = PAGE_ID_SCSI_TEMPERATURE, .bytes = 64 },
		{ .id = PAGE_ID_SCSI_START_STOP_CYCLE, .bytes = 128 },
		{ .id = PAGE_ID_SCSI_SELF_TEST, .bytes = 1024 },
		{ .id = PAGE_ID_SCSI_SS_MEDIA, .bytes = 1024 },
		{ .id = PAGE_ID_SCSI_BG_SCAN, .bytes = 512 },
		{ .id = PAGE_ID_SCSI_PROTO_SPECIFIC, .bytes = 1024 },
		{ .id = PAGE_ID_SCSI_INFO_EXCEPTION, .bytes = 64 },
	}
};

static uint32_t __smart_attribute_max(smart_buf_t *sb);
static uint32_t __smart_buffer_size(smart_h h);
static smart_map_t *__smart_map(smart_h h, smart_buf_t *sb);
static smart_page_list_t *__smart_page_list(smart_h h);
static int32_t __smart_read_pages(smart_h h, smart_buf_t *sb);

static void __smart_map_self_test_log(smart_t *s, smart_buf_t *sb, smart_map_t *sm, uint8_t log_addr);

static void __smart_map_error_log(smart_t *s, smart_buf_t *sb, smart_map_t *sm);

static void __smart_map_nvme_error_log(smart_t *s, smart_buf_t *sb, smart_map_t *sm);

static void __smart_map_sct_status(smart_t *s, smart_buf_t *sb, smart_map_t *sm);

static void __smart_map_gpl_raw(smart_buf_t *sb, smart_map_t *sm, uint8_t logaddr);

static char *
smart_proto_str(smart_protocol_e p)
{

	switch (p) {
	case SMART_PROTO_AUTO:
		return "auto";
	case SMART_PROTO_ATA:
		return "ATA";
	case SMART_PROTO_SCSI:
		return "SCSI";
	case SMART_PROTO_NVME:
		return "NVME";
	default:
		return "Unknown";
	}
}

smart_h
smart_open(smart_protocol_e protocol, char *devname)
{
	smart_t *s;

	s = device_open(protocol, devname);

	if (s) {
		dprintf("protocol %s (specified %s%s)\n",
				smart_proto_str(s->protocol),
				smart_proto_str(protocol),
				s->info.tunneled ?  ", tunneled ATA" : "");
	}

	return s;
}

void
smart_close(smart_h h)
{

	device_close(h);
}

bool
smart_supported(smart_h h)
{
	smart_t *s = h;
	bool supported = false;

	if (s) {
		supported = s->info.supported;
		dprintf("SMART is %ssupported\n", supported ? "" : "not ");
	}

	return supported;
}

smart_map_t *
smart_read(smart_h h)
{
	smart_t *s = h;
	smart_buf_t *sb = NULL;
	smart_map_t *sm = NULL;

	sb = calloc(1, sizeof(smart_buf_t));
	if (sb) {
		sb->protocol = s->protocol;

		/*
		 * Need the page list to calculate the buffer size. If one
		 * isn't specified, get the default based on the protocol.
		 */
		if (s->pg_list == NULL) {
			s->pg_list = __smart_page_list(s);
			if (!s->pg_list) {
				goto smart_read_out;
			}
		}

		sb->b = NULL;
		sb->bsize = __smart_buffer_size(s);

		if (sb->bsize != 0) {
			sb->b = malloc(sb->bsize);
		}

		if (sb->b == NULL) {
			goto smart_read_out;
		}

		if (__smart_read_pages(s, sb) < 0) {
			goto smart_read_out;
		}

		sb->attr_count = __smart_attribute_max(sb);

		sm = __smart_map(h, sb);
		if (!sm) {
			free(sb->b);
			free(sb);
			sb = NULL;
		}
	}

smart_read_out:
	if (!sm) {
		if (sb) {
			if (sb->b) {
				free(sb->b);
			}

			free(sb);
		}
	}

	return sm;
}

int32_t
smart_self_test(smart_h h, uint8_t test_type)
{
	return device_self_test(h, test_type);
}

smart_map_t *
smart_read_log(smart_h h, uint8_t log_addr, size_t size)
{
	smart_t *s = h;
	smart_buf_t *sb = NULL;
	smart_map_t *sm = NULL;

	if (s == NULL)
		return NULL;

	sb = calloc(1, sizeof(smart_buf_t));
	if (sb) {
		sb->protocol = s->protocol;

		sb->b = malloc(size);
		sb->bsize = size;

		if (sb->b == NULL) {
			free(sb);
			return NULL;
		}

		if (device_read_smart_log(h, log_addr, sb->b, size) < 0) {
			free(sb->b);
			free(sb);
			return NULL;
		}

		sb->attr_count = 32;

		sm = malloc(sizeof(smart_map_t) + (32 * sizeof(smart_attr_t)));
		if (sm) {
			memset(sm, 0, sizeof(smart_map_t) + (32 * sizeof(smart_attr_t)));
			sm->sb = sb;
			sm->count = 32;

			if (log_addr == LOG_ADDR_SELF_TEST) {
				__smart_map_self_test_log(s, sb, sm, log_addr);
			} else if (log_addr == LOG_ADDR_ERROR_LOG) {
				if (s->protocol == SMART_PROTO_NVME)
					__smart_map_nvme_error_log(s, sb, sm);
				else
					__smart_map_error_log(s, sb, sm);
			} else if (log_addr == GPL_ADDR_EXT_ERROR_LOG) {
				__smart_map_error_log(s, sb, sm);
			} else if (log_addr == GPL_ADDR_EXT_SELF_TEST_LOG) {
				__smart_map_self_test_log(s, sb, sm, log_addr);
			} else if (log_addr == GPL_ADDR_SCT_STATUS) {
				__smart_map_sct_status(s, sb, sm);
			} else if (log_addr == 0x09) {
				__smart_map_gpl_raw(sb, sm, log_addr);
			}
		} else {
			free(sb->b);
			free(sb);
		}
	}

	return sm;
}

smart_map_t *
smart_read_error_log(smart_h h)
{
	return smart_read_log(h, LOG_ADDR_ERROR_LOG, 512);
}

smart_map_t *
smart_read_log_directory(smart_h h)
{
	return smart_read_log(h, 0x00, 512);
}

smart_map_t *
smart_read_gpl_log(smart_h h, uint8_t logaddr, uint8_t page, size_t size)
{
	smart_t *s = h;
	smart_buf_t *sb = NULL;
	smart_map_t *sm = NULL;

	if (s == NULL)
		return NULL;

	sb = calloc(1, sizeof(smart_buf_t));
	if (sb) {
		sb->protocol = s->protocol;
		sb->b = malloc(size);
		sb->bsize = size;

		if (sb->b == NULL) {
			free(sb);
			return NULL;
		}

		if (device_read_log_ext(h, logaddr, page, sb->b, size) < 0) {
			free(sb->b);
			free(sb);
			return NULL;
		}

		sb->attr_count = 32;

		sm = malloc(sizeof(smart_map_t) + (32 * sizeof(smart_attr_t)));
		if (sm) {
			memset(sm, 0, sizeof(smart_map_t) + (32 * sizeof(smart_attr_t)));
			sm->sb = sb;
			sm->count = 32;

			if (logaddr == GPL_ADDR_SCT_STATUS)
				__smart_map_sct_status(s, sb, sm);
			else
				__smart_map_gpl_raw(sb, sm, logaddr);
		} else {
			free(sb->b);
			free(sb);
		}
	}

	return sm;
}

int32_t
smart_write_smart_log(smart_h h, uint8_t log_addr, void *buf, size_t size)
{
	return device_write_smart_log(h, log_addr, buf, size);
}

smart_map_t *
smart_read_sct_temp_history(smart_h h)
{
	smart_t *s = h;
	smart_buf_t *sb = NULL;
	smart_map_t *sm = NULL;
	uint8_t *cmd_buf = NULL;
	uint8_t *raw = NULL;
	uint16_t fmt_ver;

	if (s == NULL || s->protocol != SMART_PROTO_ATA)
		return NULL;

	/*
	 * Step 1: Read SCT status to check for executing commands
	 * and validate format version.
	 */
	raw = calloc(1, 512);
	if (!raw)
		return NULL;
	if (device_read_smart_log(h, GPL_ADDR_SCT_STATUS, raw, 512) < 0) {
		free(raw);
		return NULL;
	}

	fmt_ver = raw[0] | (raw[1] << 8);
	if (fmt_ver != 2 && fmt_ver != 3) {
		dprintf("Unknown SCT format version %u\n", fmt_ver);
		free(raw);
		return NULL;
	}

	uint16_t ext_status = raw[14] | (raw[15] << 8);
	if (ext_status == 0xffff) {
		free(raw);
		return NULL;
	}

	/*
	 * Step 2: Write SCT Data Table command via SMART WRITE LOG 0xE0.
	 * action_code=5 (Data Table), function_code=1 (Read), table_id=2 (Temp History).
	 */
	cmd_buf = calloc(1, 512);
	if (!cmd_buf) {
		free(raw);
		return NULL;
	}
	cmd_buf[0] = 5;   cmd_buf[1] = 0;   /* action_code */
	cmd_buf[2] = 1;   cmd_buf[3] = 0;   /* function_code */
	cmd_buf[4] = 2;   cmd_buf[5] = 0;   /* table_id */

	if (device_write_smart_log(h, GPL_ADDR_SCT_STATUS, cmd_buf, 512) < 0) {
		free(cmd_buf);
		free(raw);
		return NULL;
	}
	free(cmd_buf);

	/*
	 * Step 3: Read temperature history via SMART READ LOG 0xE1.
	 */
	memset(raw, 0, 512);
	if (device_read_smart_log(h, GPL_ADDR_SCT_TEMP_HIST, raw, 512) < 0) {
		free(raw);
		return NULL;
	}

	/*
	 * Step 4: Re-read SCT status and verify the command completed.
	 */
	cmd_buf = calloc(1, 512);
	if (!cmd_buf) {
		free(raw);
		return NULL;
	}
	if (device_read_smart_log(h, GPL_ADDR_SCT_STATUS, cmd_buf, 512) < 0) {
		free(cmd_buf);
		free(raw);
		return NULL;
	}
	uint16_t verify_ext = cmd_buf[14] | (cmd_buf[15] << 8);
	uint16_t verify_action = cmd_buf[16] | (cmd_buf[17] << 8);
	uint16_t verify_func = cmd_buf[18] | (cmd_buf[19] << 8);
	if (!(verify_ext == 0 && verify_action == 5 && verify_func == 1)) {
		dprintf("SCT verify failed: ext=0x%04x action=%u func=%u\n",
		    verify_ext, verify_action, verify_func);
		free(cmd_buf);
		free(raw);
		return NULL;
	}
	free(cmd_buf);

	sb = calloc(1, sizeof(smart_buf_t));
	if (!sb) {
		free(raw);
		return NULL;
	}
	sb->protocol = s->protocol;
	sb->b = raw;
	sb->bsize = 512;
	sb->attr_count = 1;

	sm = malloc(sizeof(smart_map_t) + sizeof(smart_attr_t));
	if (sm) {
		memset(sm, 0, sizeof(smart_map_t) + sizeof(smart_attr_t));
		sm->sb = sb;
		sm->count = 1;
		__smart_map_gpl_raw(sb, sm, GPL_ADDR_SCT_TEMP_HIST);
	} else {
		free(raw);
		free(sb);
	}

	return sm;
}

int32_t
smart_nvme_identify_ctrl(smart_h h, void *buf, size_t size)
{
	return device_nvme_identify_ctrl(h, buf, size);
}

int32_t
smart_enable(smart_h h)
{
	return device_smart_enable(h);
}

void
smart_free(smart_map_t *sm)
{
	smart_buf_t *sb = NULL;
	uint32_t i;

	if (sm == NULL)
		return;

	sb = sm->sb;

	if (sb) {
		if (sb->b) {
			free(sb->b);
			sb->b = NULL;
		}

		free(sb);
	}

	for (i = 0; i < sm->count; i++) {
		smart_map_t *tm = sm->attr[i].thresh;

		if (tm) {
			uint32_t j;
			for (j = 0; j < tm->count; j++) {
				if (tm->attr[j].raw) {
					free(tm->attr[j].raw);
				}
			}
			free(tm);
		}

		if (sm->attr[i].flags & SMART_ATTR_F_ALLOC) {
			free(sm->attr[i].description);
		}
	}

	free(sm);
}

/*
 * Format specifier for the various output types
 * Provides versions to use with libxo and without
 * TODO some of this is ATA specific
 */
#ifndef LIBXO
# define __smart_print_val(fmt, ...) 	printf(fmt, ##__VA_ARGS__)
# define VEND_STR	"Vendor\t%s\n"
# define DEV_STR	"Device\t%s\n"
# define REV_STR	"Revision\t%s\n"
# define SERIAL_STR	"Serial\t%s\n"
# define PAGE_HEX	"%#01.1x\t"
# define PAGE_DEC	"%d\t"
# define ID_HEX		"%#01.1x\t"
# define ID_DEC		"%d\t"
# define RAW_STR	"%s"
# define RAW_HEX	"%#01.1x"
# define RAW_DEC	"%d"
/* Long integer version of the format macro */
# define RAW_LHEX	"%#01.1" PRIx64
# define RAW_LDEC	"%" PRId64
# define THRESH_HEX	"\t%#02.2x\t%#01.1x\t%#01.1x\t%#01.1x"
# define THRESH_DEC	"\t%d\t%d\t%d\t%d"
# define DESC_STR	"%s"
#else
# define __smart_print_val(fmt, ...) 	 xo_emit(fmt, ##__VA_ARGS__)
# define VEND_STR	"{L:Vendor}{P:\t}{:vendor/%s}\n"
# define DEV_STR	"{L:Device}{P:\t}{:device/%s}\n"
# define REV_STR	"{L:Revision}{P:\t}{:rev/%s}\n"
# define SERIAL_STR	"{L:Serial}{P:\t}{:serial/%s}\n"
# define PAGE_HEX	"{k:page/%#01.1x}{P:\t}"
# define PAGE_DEC	"{k:page/%d}{P:\t}"
# define ID_HEX		"{k:id/%#01.1x}{P:\t}"
# define ID_DEC		"{k:id/%d}{P:\t}"
# define RAW_STR	"{k:raw/%s}"
# define RAW_HEX	"{k:raw/%#01.1x}"
# define RAW_DEC	"{k:raw/%d}"
/* Long integer version of the format macro */
# define RAW_LHEX	"{k:raw/%#01.1" PRIx64 "}"
# define RAW_LDEC	"{k:raw/%" PRId64 "}"
# define THRESH_HEX	"{P:\t}{k:threshold/%#02.2x\t%#01.1x\t%#01.1x\t%#01.1x}"
# define THRESH_DEC	"{P:\t}{k:threshold/%d\t%d\t%d\t%d}"
# define DESC_STR	"{:description}{P:\t}"
#endif


/* Convert an 128-bit unsigned integer to a string */
static char *
__smart_u128_str(smart_attr_t *sa)
{
	/* Max size is log10(x) = log2(x) / log2(10) ~= log2(x) / 3.322 */
#define MAX_LEN (128 / 3 + 1 + 1)
	static char s[MAX_LEN];
	char *p = s + MAX_LEN - 1;
	uint32_t *a = (uint32_t *)sa->raw;
	uint64_t r, d;
	uint32_t last = 0;

	*p-- = '\0';

	do {
		r = a[3];

		d = r / 10;
		r = ((r - d * 10) << 32) + a[2];
		a[3] = d;

		d = r / 10;
		r = ((r - d * 10) << 32) + a[1];
		a[2] = d;

		d = r / 10;
		r = ((r - d * 10) << 32) + a[0];
		a[1] = d;

		d = r / 10;
		r = r - d * 10;
		a[0] = d;

		*p-- = '0' + r;
	} while (a[0] || a[1] || a[2] || a[3]);

	p++;

	while ((*p == '0') && (p < &s[sizeof(s) - 2]))
		p++;

	return p;
}

static void
__smart_print_thresh(smart_map_t *tm, uint32_t flags)
{
	bool do_hex = false;
	bool do_thresh = false;

	if (!tm) {
		return;
	}

	if (flags & SMART_OPEN_F_HEX)
		do_hex = true;

	if (flags & SMART_OPEN_F_THRESH)
		do_thresh = true;

	if (do_thresh && tm && tm->count >= 4) {
		__smart_print_val(do_hex ? THRESH_HEX : THRESH_DEC,
				*((uint16_t *)tm->attr[0].raw),
				*((uint8_t *)tm->attr[1].raw),
				*((uint8_t *)tm->attr[2].raw),
				*((uint8_t *)tm->attr[3].raw));
	}
}

/* Does the attribute match one requested by the caller? */
static bool
__smart_attr_match(smart_matches_t *match, smart_attr_t *attr)
{
	uint32_t i;

	assert((match != NULL) && (attr != NULL));

	for (i = 0; i < match->count; i++) {
		if ((match->m[i].page != -1) && (match->m[i].page != attr->page))
			continue;

		if (match->m[i].id == attr->id)
			return true;
	}

	return false;
}

void
smart_print(smart_h h, smart_map_t *sm, smart_matches_t *which, uint32_t flags)
{
	uint32_t i;
	const char *fmt, *lfmt;
	bool do_hex = false, do_descr = false;
	uint32_t bytes = 0;

	if (!sm) {
		return;
	}

	if (flags & SMART_OPEN_F_HEX)
		do_hex = true;
	if (flags & SMART_OPEN_F_DESCR)
		do_descr = true;

#ifdef LIBXO
	xo_open_container("attributes");
	xo_open_list("attribute");
#endif
	for (i = 0; i < sm->count; i++) {
		/* If we're printing a specific attribute, is this it? */
		if ((which != NULL) && !__smart_attr_match(which, &sm->attr[i])) {
			continue;
		}

#ifdef LIBXO
		xo_open_instance("attribute");
#endif
		/* Print the page / attribute ID if selecting all attributes */
		if (which == NULL) {
			if (do_descr && (sm->attr[i].description != NULL))
				__smart_print_val(DESC_STR, sm->attr[i].description);
			else
				__smart_print_val(do_hex ? PAGE_HEX : PAGE_DEC, sm->attr[i].page);
				__smart_print_val(do_hex ? ID_HEX : ID_DEC, sm->attr[i].id);
		}

		bytes = sm->attr[i].bytes;

		/* Print the attribute based on its size */
		if (sm->attr[i].flags & SMART_ATTR_F_STR) {
			__smart_print_val(RAW_STR, (char *)sm->attr[i].raw);
		} else if (bytes > 8) {
			if (do_hex)
				;
			else
				__smart_print_val(RAW_STR,
				    __smart_u128_str(&sm->attr[i]));

		} else if (bytes > 4) {
			uint64_t v64 = 0;
			uint64_t mask = UINT64_MAX;

			bcopy(sm->attr[i].raw, &v64, bytes);

			if (sm->attr[i].flags & SMART_ATTR_F_BE) {
				v64 = be64toh(v64);
			} else {
				v64 = le64toh(v64);
			}

			mask >>= 8 * (sizeof(uint64_t) - bytes);

			v64 &= mask;

			__smart_print_val(do_hex ? RAW_LHEX : RAW_LDEC, v64);

		} else if (bytes > 2) {
			uint32_t v32 = 0;
			uint32_t mask = UINT32_MAX;

			bcopy(sm->attr[i].raw, &v32, bytes);

			if (sm->attr[i].flags & SMART_ATTR_F_BE) {
				v32 = be32toh(v32);
			} else {
				v32 = le32toh(v32);
			}

			mask >>= 8 * (sizeof(uint32_t) - bytes);

			v32 &= mask;

			__smart_print_val(do_hex ? RAW_HEX : RAW_DEC, v32);

		} else if (bytes > 1) {
			uint16_t v16 = 0;
			uint16_t mask = UINT16_MAX;

			bcopy(sm->attr[i].raw, &v16, bytes);

			if (sm->attr[i].flags & SMART_ATTR_F_BE) {
				v16 = be16toh(v16);
			} else {
				v16 = le16toh(v16);
			}

			mask >>= 8 * (sizeof(uint16_t) - bytes);

			v16 &= mask;

			__smart_print_val(do_hex ? RAW_HEX : RAW_DEC, v16);

		} else if (bytes > 0) {
			uint8_t v8 = *((uint8_t *)sm->attr[i].raw);

			__smart_print_val(do_hex ? RAW_HEX : RAW_DEC, v8);
		}

		__smart_print_thresh(sm->attr[i].thresh, flags);

		__smart_print_val("\n");

#ifdef LIBXO
		xo_close_instance("attribute");
#endif
	}
#ifdef LIBXO
	xo_close_list("attribute");
	xo_close_container("attributes");
#endif
}

void
smart_print_device_info(smart_h h)
{
	smart_t *s = h;

	if (!s) {
		return;
	}

	if (*s->info.vendor != '\0')
		__smart_print_val(VEND_STR, s->info.vendor);
	if (*s->info.device != '\0')
		__smart_print_val(DEV_STR, s->info.device);
	if (*s->info.rev != '\0')
		__smart_print_val(REV_STR, s->info.rev);
	if (*s->info.serial != '\0')
		__smart_print_val(SERIAL_STR, s->info.serial);
}

static uint32_t
__smart_attr_max_ata(smart_buf_t *sb)
{
	uint32_t max = 0;

	if (sb) {
		max = 30;
	}

	return max;
}

static uint32_t
__smart_attr_max_nvme(smart_buf_t *sb)
{
	uint32_t max = 0;

	if (sb) {
		max = 512;
	}

	return max;
}

static uint32_t
__smart_attr_max_scsi(smart_buf_t *sb)
{
	uint32_t max = 0;

	if (sb) {
		max = 512;
	}

	return max;
}

static uint32_t
__smart_attribute_max(smart_buf_t *sb)
{
	uint32_t count = 0;

	if (sb != NULL) {
		switch (sb->protocol) {
		case SMART_PROTO_ATA:
			count = __smart_attr_max_ata(sb) + 3;
			break;
		case SMART_PROTO_NVME:
			count = __smart_attr_max_nvme(sb);
			break;
		case SMART_PROTO_SCSI:
			count = __smart_attr_max_scsi(sb);
			break;
		default:
			;
		}
	}

	return count;
}

/**
 * Return the total buffer size needed by the protocol's page list
 */
static uint32_t
__smart_buffer_size(smart_h h)
{
	smart_t *s = h;
	uint32_t size = 0;

	if ((s != NULL) && (s->pg_list != NULL)) {
		smart_page_list_t *plist = s->pg_list;
		uint32_t p = 0;

		for (p = 0; p < plist->pg_count; p++) {
			size += plist->pages[p].bytes;
		}
	}

	return size;
}

static void __smart_map_ata_read_data(smart_map_t *sm, void *buf, size_t bsize, uint8_t *threshold_by_id);

static smart_map_t *
__smart_make_attr_thresh(uint8_t id, uint8_t threshold)
{
	smart_map_t *thm;
	uint8_t *tbuf;

	thm = calloc(1, sizeof(smart_map_t) + sizeof(smart_attr_t));
	if (!thm)
		return NULL;

	tbuf = malloc(2);
	if (!tbuf) {
		free(thm);
		return NULL;
	}
	tbuf[0] = 0;
	tbuf[1] = threshold;

	thm->count = 1;
	thm->attr[0].page = 0;
	thm->attr[0].id = id;
	thm->attr[0].bytes = 2;
	thm->attr[0].flags = 0;
	thm->attr[0].raw = tbuf;
	thm->attr[0].thresh = NULL;

	return thm;
}

/* Map SMART READ DATA attributes */
static void
__smart_map_ata_read_data(smart_map_t *sm, void *buf, size_t bsize, uint8_t *threshold_by_id)
{
	uint8_t *b = NULL;
	uint8_t *b_end = NULL;
	uint32_t max_attr = 0;
	uint32_t a;

	max_attr = __smart_attr_max_ata(sm->sb);
	a = sm->count;

	b = buf;

	b += 2;

	b_end = b + (max_attr * 12);

	while (b < b_end) {
		if (*b != 0) {
			if ((a - sm->count) >= max_attr) {
				warnx("More attributes (%d) than fit in map",
						a - sm->count);
				break;
			}

			sm->attr[a].page = PAGE_ID_ATA_SMART_READ_DATA;
			sm->attr[a].id = b[0];
			sm->attr[a].description = __smart_ata_desc(
			    PAGE_ID_ATA_SMART_READ_DATA, sm->attr[a].id);
			sm->attr[a].bytes = 12;
			sm->attr[a].flags = 0;
			sm->attr[a].raw = b;
			if (threshold_by_id && threshold_by_id[b[0]] > 0) {
				sm->attr[a].thresh = __smart_make_attr_thresh(
				    b[0], threshold_by_id[b[0]]);
			} else {
				sm->attr[a].thresh = NULL;
			}

			a++;
		}

		b += 12;
	}

	sm->attr[a].page = PAGE_ID_ATA_SMART_READ_DATA;
	sm->attr[a].id = 255;
	sm->attr[a].description = "Self-Test Status";
	sm->attr[a].bytes = 1;
	sm->attr[a].flags = 0;
	sm->attr[a].raw = b + 1;
	sm->attr[a].thresh = NULL;
	a++;

	sm->attr[a].page = PAGE_ID_ATA_SMART_READ_DATA;
	sm->attr[a].id = 254;
	sm->attr[a].description = "SMART Capability";
	sm->attr[a].bytes = 2;
	sm->attr[a].flags = 0;
	sm->attr[a].raw = b + 6;
	sm->attr[a].thresh = NULL;
	a++;

	sm->count = a;
}

static void
__smart_map_ata_return_status(smart_map_t *sm, void *buf, size_t bsize)
{
	uint8_t *b = NULL;
	uint32_t a;

	a = sm->count;

	b = buf;

	sm->attr[a].page = PAGE_ID_ATA_SMART_RET_STATUS;
	sm->attr[a].id = 0;
	sm->attr[a].description = __smart_ata_desc(PAGE_ID_ATA_SMART_RET_STATUS,
	    sm->attr[a].id);
	sm->attr[a].bytes = 1;
	sm->attr[a].flags = 0;
	sm->attr[a].raw = b;
	sm->attr[a].thresh = NULL;

	a++;

	sm->count = a;
}

static void
__smart_map_ata(smart_h h, smart_buf_t *sb, smart_map_t *sm)
{
	smart_t *s = h;
	smart_page_list_t *pg_list = NULL;
	uint8_t *b = NULL;
	uint8_t *thresh_buf = NULL;
	uint8_t threshold_by_id[256];
	uint32_t p;

	pg_list = s->pg_list;
	b = sb->b;

	memset(threshold_by_id, 0, sizeof(threshold_by_id));

	for (p = 0; p < pg_list->pg_count; p++) {
		if (pg_list->pages[p].id == PAGE_ID_ATA_SMART_READ_THRESHOLDS) {
			thresh_buf = b;
			b += pg_list->pages[p].bytes;
			continue;
		}
		b += pg_list->pages[p].bytes;
	}

	if (thresh_buf) {
		uint8_t *tb = thresh_buf + 2;
		uint32_t i;
		for (i = 0; i < 30; i++) {
			uint8_t id = tb[0];
			uint8_t thresh = tb[1];
			if (id > 0 && id < 255) {
				threshold_by_id[id] = thresh;
			}
			tb += 12;
		}
	}

	b = sb->b;
	sm->count = 0;

	for (p = 0; p < pg_list->pg_count; p++) {
		switch (pg_list->pages[p].id) {
		case PAGE_ID_ATA_SMART_READ_DATA:
			__smart_map_ata_read_data(sm, b, pg_list->pages[p].bytes,
			    threshold_by_id);
			break;
		case PAGE_ID_ATA_SMART_READ_THRESHOLDS:
			break;
		case PAGE_ID_ATA_SMART_RET_STATUS:
			__smart_map_ata_return_status(sm, b, pg_list->pages[p].bytes);
			break;
		}

		b += pg_list->pages[p].bytes;
	}
}

#ifndef ARRAYLEN
#define ARRAYLEN(p) sizeof(p)/sizeof(p[0])
#endif

#define NVME_VS(mjr,mnr,ter) (((mjr) << 16) | ((mnr) << 8) | (ter))
#define NVME_VS_1_0	NVME_VS(1,0,0)
#define NVME_VS_1_1	NVME_VS(1,1,0)
#define NVME_VS_1_2	NVME_VS(1,2,0)
#define NVME_VS_1_2_1	NVME_VS(1,2,1)
#define NVME_VS_1_3	NVME_VS(1,3,0)
#define NVME_VS_1_4	NVME_VS(1,4,0)
struct {
	uint32_t off;		/* buffer offset */
	uint32_t bytes;		/* size in bytes */
	uint32_t ver;		/* first version available */
	char *description;
} __smart_nvme_values[] = {
	{   0,  1, NVME_VS_1_0, "Critical Warning" },
	{   1,  2, NVME_VS_1_0, "Composite Temperature" },
	{   3,  1, NVME_VS_1_0, "Available Spare" },
	{   4,  1, NVME_VS_1_0, "Available Spare Threshold" },
	{   5,  1, NVME_VS_1_0, "Percentage Used" },
	{   6,  1, NVME_VS_1_4, "Endurance Group Critical Warning Summary" },
	{  32, 16, NVME_VS_1_0, "Data Units Read" },
	{  48, 16, NVME_VS_1_0, "Data Units Written" },
	{  64, 16, NVME_VS_1_0, "Host Read Commands" },
	{  80, 16, NVME_VS_1_0, "Host Write Commands" },
	{  96, 16, NVME_VS_1_0, "Controller Busy Time" },
	{ 112, 16, NVME_VS_1_0, "Power Cycles" },
	{ 128, 16, NVME_VS_1_0, "Power On Hours" },
	{ 144, 16, NVME_VS_1_0, "Unsafe Shutdowns" },
	{ 160, 16, NVME_VS_1_0, "Media and Data Integrity Errors" },
	{ 176, 16, NVME_VS_1_0, "Number of Error Information Log Entries" },
	{ 192,  4, NVME_VS_1_2, "Warning Composite Temperature Time" },
	{ 196,  4, NVME_VS_1_2, "Critical Composite Temperature Time" },
	{ 200,  2, NVME_VS_1_2, "Temperature Sensor 1" },
	{ 202,  2, NVME_VS_1_2, "Temperature Sensor 2" },
	{ 204,  2, NVME_VS_1_2, "Temperature Sensor 3" },
	{ 206,  2, NVME_VS_1_2, "Temperature Sensor 4" },
	{ 208,  2, NVME_VS_1_2, "Temperature Sensor 5" },
	{ 210,  2, NVME_VS_1_2, "Temperature Sensor 6" },
	{ 212,  2, NVME_VS_1_2, "Temperature Sensor 7" },
	{ 214,  2, NVME_VS_1_2, "Temperature Sensor 8" },
	{ 216,  4, NVME_VS_1_3, "Thermal Management Temperature 1 Transition Count" },
	{ 220,  4, NVME_VS_1_3, "Thermal Management Temperature 2 Transition Count" },
	{ 224,  4, NVME_VS_1_3, "Total Time For Thermal Management Temperature 1" },
	{ 228,  4, NVME_VS_1_3, "Total Time For Thermal Management Temperature 2" },
};

/**
 * NVMe doesn't define attribute IDs like ATA does, but we can
 * approximate this behavior by treating the byte offset as the
 * attribute ID.
 */
static void
__smart_map_nvme(smart_buf_t *sb, smart_map_t *sm)
{
	uint8_t *b = NULL;
	uint32_t vs = NVME_VS_1_0;	// XXX assume device is 1.0
	uint32_t i, a;

	sm->count = 0;
	b = sb->b;

	for (i = 0, a = 0; i < ARRAYLEN(__smart_nvme_values); i++) {
		if (vs >= __smart_nvme_values[i].ver) {
			sm->attr[a].page = 0x2;
			sm->attr[a].id = __smart_nvme_values[i].off;
			sm->attr[a].description = __smart_nvme_values[i].description;
			sm->attr[a].bytes = __smart_nvme_values[i].bytes;
			sm->attr[a].flags = 0;
			sm->attr[a].raw = b + __smart_nvme_values[i].off;
			sm->attr[a].thresh = NULL;

			a++;
		}
	}

	sm->count = a;
}

static void
__smart_map_self_test_log(smart_t *s, smart_buf_t *sb, smart_map_t *sm, uint8_t log_addr)
{
	uint8_t *b = sb->b;
	uint32_t a = 0;

	sm->count = 0;

	if (s->protocol == SMART_PROTO_NVME && sb->bsize >= 4) {
		uint8_t cur_op = b[0] & 0x0F;
		uint8_t cur_pct = b[1] & 0x7F;

		sm->attr[a].page = NVME_LOG_SELF_TEST;
		sm->attr[a].id = 0;
		sm->attr[a].description = cur_op ? "Self-Test In Progress" : "Self-Test Idle";
		sm->attr[a].bytes = 1;
		sm->attr[a].flags = 0;
		sm->attr[a].raw = b;
		sm->attr[a].thresh = NULL;
		a++;

		sm->attr[a].page = NVME_LOG_SELF_TEST;
		sm->attr[a].id = 1;
		sm->attr[a].description = "Current Completion";
		sm->attr[a].bytes = 1;
		sm->attr[a].flags = 0;
		sm->attr[a].raw = b + 1;
		sm->attr[a].thresh = NULL;
		a++;

		if (sb->bsize >= 564) {
			uint32_t i;
			for (i = 0; i < 20 && a < 30; i++) {
				uint8_t *entry = b + 4 + (i * 28);
				uint8_t status = entry[0];
				uint8_t op = (status >> 4) & 0x0F;

				if (op == 0x0 && (status & 0x0F) == 0x0F)
					continue;

				sm->attr[a].page = NVME_LOG_SELF_TEST;
				sm->attr[a].id = 2 + i;
				sm->attr[a].description = "Self-Test Result";
				sm->attr[a].bytes = 28;
				sm->attr[a].flags = 0;
				sm->attr[a].raw = entry;
				sm->attr[a].thresh = NULL;
				a++;
			}
		}
	} else if (s->protocol == SMART_PROTO_ATA && sb->bsize >= 512) {
		uint32_t entry_size = (log_addr == GPL_ADDR_EXT_SELF_TEST_LOG) ? 26 : 24;
		uint32_t max_entries = (log_addr == GPL_ADDR_EXT_SELF_TEST_LOG) ? 19 : 21;
		uint32_t entry_offset = (log_addr == GPL_ADDR_EXT_SELF_TEST_LOG) ? 0 : 2;
		uint8_t most_recent = (log_addr == GPL_ADDR_EXT_SELF_TEST_LOG) ? 0 : b[508];
		int i;

		for (i = max_entries - 1; i >= 0 && a < 30; i--) {
			int j = (i + most_recent) % max_entries;
			uint8_t *entry = b + entry_offset + (j * entry_size);
			uint8_t type = entry[0];
			uint8_t status = entry[1];

			if (type == 0 && status == 0)
				continue;

			sm->attr[a].page = log_addr;
			sm->attr[a].id = max_entries - i;
			sm->attr[a].description = "Self-Test Result";
			sm->attr[a].bytes = entry_size;
			sm->attr[a].flags = 0;
			sm->attr[a].raw = entry;
			sm->attr[a].thresh = NULL;
			a++;
		}
	}

	sm->count = a;
}

static void
__smart_map_error_log(smart_t *s, smart_buf_t *sb, smart_map_t *sm)
{
	uint8_t *b = sb->b;
	uint32_t a = 0;
	uint8_t err_idx;
	int i;

	sm->count = 0;

	if (sb->bsize < 512)
		return;

	err_idx = b[1];

	for (i = 0; i < 5 && a < 30; i++) {
		int entry_idx = (err_idx + i) % 5;
		uint8_t *entry = b + 2 + (entry_idx * 90);

		if (entry[0] == 0 && entry[2] == 0)
			continue;

		sm->attr[a].page = LOG_ADDR_ERROR_LOG;
		sm->attr[a].id = i;
		sm->attr[a].description = "ATA Error";
		sm->attr[a].bytes = 90;
		sm->attr[a].flags = 0;
		sm->attr[a].raw = entry;
		sm->attr[a].thresh = NULL;
		a++;
	}

	sm->count = a;
}

static void
__smart_map_nvme_error_log(smart_t *s, smart_buf_t *sb, smart_map_t *sm)
{
	uint8_t *b = sb->b;
	uint32_t a = 0;
	uint32_t entry_size = 64;
	uint32_t max_entries = 5;
	uint32_t i;

	sm->count = 0;

	if (sb->bsize < entry_size)
		return;

	if (sb->bsize / entry_size < max_entries)
		max_entries = (uint32_t)(sb->bsize / entry_size);

	for (i = 0; i < max_entries && a < 30; i++) {
		uint8_t *entry = b + (i * entry_size);
		uint64_t err_cnt = (uint64_t)entry[0] | ((uint64_t)entry[1] << 8) |
			((uint64_t)entry[2] << 16) | ((uint64_t)entry[3] << 24) |
			((uint64_t)entry[4] << 32) | ((uint64_t)entry[5] << 40) |
			((uint64_t)entry[6] << 48) | ((uint64_t)entry[7] << 56);

		if (err_cnt == 0)
			continue;

		sm->attr[a].page = NVME_LOG_ERROR;
		sm->attr[a].id = i;
		sm->attr[a].description = "NVMe Error";
		sm->attr[a].bytes = entry_size;
		sm->attr[a].flags = 0;
		sm->attr[a].raw = entry;
		sm->attr[a].thresh = NULL;
		a++;
	}

	sm->count = a;
}

static void
__smart_map_sct_status(smart_t *s, smart_buf_t *sb, smart_map_t *sm)
{
	uint8_t *b = sb->b;
	uint32_t a = 0;

	sm->count = 0;

	if (sb->bsize < 512)
		return;

	sm->attr[a].page = GPL_ADDR_SCT_STATUS;
	sm->attr[a].id = 0;
	sm->attr[a].description = "SCT Format Version";
	sm->attr[a].bytes = 2;
	sm->attr[a].flags = 0;
	sm->attr[a].raw = b;
	sm->attr[a].thresh = NULL;
	a++;

	sm->attr[a].page = GPL_ADDR_SCT_STATUS;
	sm->attr[a].id = 1;
	sm->attr[a].description = "SCT Device State";
	sm->attr[a].bytes = 1;
	sm->attr[a].flags = 0;
	sm->attr[a].raw = b + 10;
	sm->attr[a].thresh = NULL;
	a++;

	sm->attr[a].page = GPL_ADDR_SCT_STATUS;
	sm->attr[a].id = 2;
	sm->attr[a].description = "Current Temperature";
	sm->attr[a].bytes = 1;
	sm->attr[a].flags = 0;
	sm->attr[a].raw = b + 200;
	sm->attr[a].thresh = NULL;
	a++;

	sm->attr[a].page = GPL_ADDR_SCT_STATUS;
	sm->attr[a].id = 3;
	sm->attr[a].description = "Min/Max Temperature (this cycle)";
	sm->attr[a].bytes = 2;
	sm->attr[a].flags = 0;
	sm->attr[a].raw = b + 201;
	sm->attr[a].thresh = NULL;
	a++;

	sm->attr[a].page = GPL_ADDR_SCT_STATUS;
	sm->attr[a].id = 4;
	sm->attr[a].description = "Lifetime Min/Max Temperature";
	sm->attr[a].bytes = 2;
	sm->attr[a].flags = 0;
	sm->attr[a].raw = b + 203;
	sm->attr[a].thresh = NULL;
	a++;

	sm->attr[a].page = GPL_ADDR_SCT_STATUS;
	sm->attr[a].id = 5;
	sm->attr[a].description = "Over Temperature Limit Count";
	sm->attr[a].bytes = 4;
	sm->attr[a].flags = 0;
	sm->attr[a].raw = b + 206;
	sm->attr[a].thresh = NULL;
	a++;

	sm->attr[a].page = GPL_ADDR_SCT_STATUS;
	sm->attr[a].id = 6;
	sm->attr[a].description = "Under Temperature Limit Count";
	sm->attr[a].bytes = 4;
	sm->attr[a].flags = 0;
	sm->attr[a].raw = b + 210;
	sm->attr[a].thresh = NULL;
	a++;

	sm->attr[a].page = GPL_ADDR_SCT_STATUS;
	sm->attr[a].id = 7;
	sm->attr[a].description = "Smart Status";
	sm->attr[a].bytes = 2;
	sm->attr[a].flags = 0;
	sm->attr[a].raw = b + 214;
	sm->attr[a].thresh = NULL;
	a++;

	sm->attr[a].page = GPL_ADDR_SCT_STATUS;
	sm->attr[a].id = 8;
	sm->attr[a].description = "Max Operation Limit";
	sm->attr[a].bytes = 1;
	sm->attr[a].flags = 0;
	sm->attr[a].raw = b + 205;
	sm->attr[a].thresh = NULL;
	a++;

	sm->attr[a].page = GPL_ADDR_SCT_STATUS;
	sm->attr[a].id = 9;
	sm->attr[a].description = "SCT Version";
	sm->attr[a].bytes = 2;
	sm->attr[a].flags = 0;
	sm->attr[a].raw = b + 2;
	sm->attr[a].thresh = NULL;
	a++;

	sm->attr[a].page = GPL_ADDR_SCT_STATUS;
	sm->attr[a].id = 10;
	sm->attr[a].description = "SCT Spec";
	sm->attr[a].bytes = 2;
	sm->attr[a].flags = 0;
	sm->attr[a].raw = b + 4;
	sm->attr[a].thresh = NULL;
	a++;

	sm->attr[a].page = GPL_ADDR_SCT_STATUS;
	sm->attr[a].id = 11;
	sm->attr[a].description = "SCT Status Flags";
	sm->attr[a].bytes = 4;
	sm->attr[a].flags = 0;
	sm->attr[a].raw = b + 6;
	sm->attr[a].thresh = NULL;
	a++;

	sm->attr[a].page = GPL_ADDR_SCT_STATUS;
	sm->attr[a].id = 12;
	sm->attr[a].description = "SCT Ext Status Code";
	sm->attr[a].bytes = 2;
	sm->attr[a].flags = 0;
	sm->attr[a].raw = b + 14;
	sm->attr[a].thresh = NULL;
	a++;

	sm->attr[a].page = GPL_ADDR_SCT_STATUS;
	sm->attr[a].id = 13;
	sm->attr[a].description = "SCT Action Code";
	sm->attr[a].bytes = 2;
	sm->attr[a].flags = 0;
	sm->attr[a].raw = b + 16;
	sm->attr[a].thresh = NULL;
	a++;

	sm->attr[a].page = GPL_ADDR_SCT_STATUS;
	sm->attr[a].id = 14;
	sm->attr[a].description = "SCT Function Code";
	sm->attr[a].bytes = 2;
	sm->attr[a].flags = 0;
	sm->attr[a].raw = b + 18;
	sm->attr[a].thresh = NULL;
	a++;

	sm->attr[a].page = GPL_ADDR_SCT_STATUS;
	sm->attr[a].id = 15;
	sm->attr[a].description = "SCT LBA Current";
	sm->attr[a].bytes = 8;
	sm->attr[a].flags = 0;
	sm->attr[a].raw = b + 40;
	sm->attr[a].thresh = NULL;
	a++;

	sm->attr[a].page = GPL_ADDR_SCT_STATUS;
	sm->attr[a].id = 16;
	sm->attr[a].description = "SCT Min ERC Time";
	sm->attr[a].bytes = 2;
	sm->attr[a].flags = 0;
	sm->attr[a].raw = b + 216;
	sm->attr[a].thresh = NULL;
	a++;

	sm->count = a;
}

static void
__smart_map_gpl_raw(smart_buf_t *sb, smart_map_t *sm, uint8_t logaddr)
{
	sm->attr[0].page = logaddr;
	sm->attr[0].id = 0;
	sm->attr[0].description = "GPL Data";
	sm->attr[0].bytes = sb->bsize;
	sm->attr[0].flags = 0;
	sm->attr[0].raw = sb->b;
	sm->attr[0].thresh = NULL;
	sm->count = 1;
}

/*
 * Create a SMART map for SCSI error counter pages
 *
 * Several SCSI log pages have a similar format for the error counter log
 * pages
 */
static void
__smart_map_scsi_err_page(smart_map_t *sm, void *b, size_t bsize)
{
	struct scsi_err_page {
		uint8_t page_code;
		uint8_t subpage_code;
		uint16_t page_length;
		uint8_t param[];
	} __attribute__((packed)) *err = b;
	struct scsi_err_counter_param {
		uint16_t	code;
		uint8_t		format:2,
				tmc:2,
				etc:1,
				tsd:1,
				:1,
				du:1;
		uint8_t		length;
		uint8_t		counter[];
	} __attribute__((packed)) *param = NULL;
	uint32_t a, p, page_length;
	char *cmd = NULL, *desc = NULL;

	switch (err->page_code) {
	case PAGE_ID_SCSI_WRITE_ERR:
		cmd = "Write";
		break;
	case PAGE_ID_SCSI_READ_ERR:
		cmd = "Read";
		break;
	case PAGE_ID_SCSI_VERIFY_ERR:
		cmd = "Verify";
		break;
	case PAGE_ID_SCSI_NON_MEDIUM_ERR:
		cmd = "Non-Medium";
		break;
	default:
		dprintf("Unknown command %#x\n", err->page_code);
		cmd = "Unknown";
		break;
	}

	a = sm->count;

	p = 0;
	page_length = be16toh(err->page_length);

	/* Validate page length fits within the provided buffer */
	if (page_length + 4 > bsize) {
		return;
	}

	while (p < page_length) {
		/* Validate that the parameter entry header fits within the page */
		if (p + 4 > page_length) {
			break;
		}

		param = (struct scsi_err_counter_param *) (err->param + p);

		/* Validate that the full parameter (header + length) fits within the page */
		if (p + 4 + param->length > page_length) {
			break;
		}

		sm->attr[a].page = err->page_code;
		sm->attr[a].id = be16toh(param->code);
		desc = __smart_scsi_err_desc(sm->attr[a].id);
		if (desc != NULL) {
			size_t bytes;
			char *str;

			bytes = snprintf(NULL, 0, "%s %s", cmd, desc);
			str = malloc(bytes + 1);
			if (str != NULL) {
				snprintf(str, bytes + 1, "%s %s", cmd, desc);
				sm->attr[a].description = str;
				sm->attr[a].flags |= SMART_ATTR_F_ALLOC;
			}
		}
		sm->attr[a].bytes = param->length;
		sm->attr[a].flags |= SMART_ATTR_F_BE;
		sm->attr[a].raw = param->counter;
		sm->attr[a].thresh = NULL;

		p += 4 + param->length;

		a++;
		if (a >= sm->sb->attr_count) {
			break;
		}
	}
	
	sm->count = a;
}

static void
__smart_map_scsi_last_err(smart_map_t *sm, void *b, size_t bsize)
{
	struct scsi_last_n_error_event_page {
		uint8_t page_code:6,
			spf:1,
			ds:1;
		uint8_t	subpage_code;
		uint16_t page_length;
		uint8_t event[];
	} __attribute__((packed)) *lastn = b;
	struct scsi_last_n_error_event {
		uint16_t	code;
		uint8_t		format:2,
				tmc:2,
				etc:1,
				tsd:1,
				:1,
				du:1;
		uint8_t		length;
		uint8_t		data[];
	} __attribute__((packed)) *event = NULL;
	uint32_t a, p, page_length;

	a = sm->count;

	p = 0;
	page_length = be16toh(lastn->page_length);

	if (page_length + 4 > bsize) {
		return;
	}

	while (p < page_length) {
		if (p + 4 > page_length) {
			break;
		}

		event = (struct scsi_last_n_error_event *) (lastn->event + p);

		if (p + 4 + event->length > page_length) {
			break;
		}

		sm->attr[a].page = lastn->page_code;
		sm->attr[a].id = be16toh(event->code);
		sm->attr[a].bytes = event->length;
		sm->attr[a].flags = SMART_ATTR_F_BE;
		sm->attr[a].raw = event->data;
		sm->attr[a].thresh = NULL;

		p += 4 + event->length;

		a++;
		if (a >= sm->sb->attr_count) {
			break;
		}
	}
	
	sm->count = a;
}

static void
__smart_map_scsi_temp(smart_map_t *sm, void *b, size_t bsize)
{
	struct scsi_temperature_log_page {
		uint8_t page_code;
		uint8_t subpage_code;
		uint16_t page_length;
		struct scsi_temperature_log_entry {
			uint16_t code;
			uint8_t control;
			uint8_t length;
			uint8_t	rsvd;
			uint8_t temperature;
		} param[];
	} __attribute__((packed)) *temp = b;
	uint32_t a, p, count, page_length;

	if (bsize < 4) {
		return;
	}

	page_length = be16toh(temp->page_length);
	if (page_length + 4 > bsize) {
		return;
	}

	count = page_length / sizeof(struct scsi_temperature_log_entry);

	a = sm->count;

	for (p = 0; p < count; p++) {
		uint16_t code = be16toh(temp->param[p].code);
		switch (code) {
		case 0:
		case 1:
			sm->attr[a].page = temp->page_code;
			sm->attr[a].id = be16toh(temp->param[p].code);
			sm->attr[a].description = code == 0 ? "Temperature" : "Reference Temperature";
			sm->attr[a].bytes = 1;
			sm->attr[a].flags = 0;
			sm->attr[a].raw = &(temp->param[p].temperature);
			sm->attr[a].thresh = NULL;
			a++;
			if (a >= sm->sb->attr_count) {
				break;
			}
			break;
		default:
			break;
		}
	}

	sm->count = a;
}

static void
__smart_map_scsi_start_stop(smart_map_t *sm, void *b, size_t bsize)
{
	struct scsi_start_stop_page {
		uint8_t page_code;
#define START_STOP_CODE_DATE_MFG	0x0001
#define START_STOP_CODE_DATE_ACCTN	0x0002
#define START_STOP_CODE_CYCLES_LIFE	0x0003
#define START_STOP_CODE_CYCLES_ACCUM	0x0004
#define START_STOP_CODE_LOAD_LIFE	0x0005
#define START_STOP_CODE_LOAD_ACCUM	0x0006
		uint8_t subpage_code;
		uint16_t page_length;
		uint8_t param[];
	} __attribute__((packed)) *sstop = b;
	struct scsi_start_stop_param {
		uint16_t code;
		uint8_t	format:2,
			tmc:2,
			etc:1,
			tsd:1,
			:1,
			du:1;
		uint8_t length;
		uint8_t data[];
	} __attribute__((packed)) *param;
	uint32_t a, p, page_length;

	a = sm->count;

	p = 0;
	page_length = be16toh(sstop->page_length);

	if (page_length + 4 > bsize) {
		return;
	}

	while (p < page_length) {
		if (p + 4 > page_length) {
			break;
		}

		param = (struct scsi_start_stop_param *) (sstop->param + p);

		if (p + 4 + param->length > page_length) {
			break;
		}

		sm->attr[a].page = sstop->page_code;
		sm->attr[a].id = be16toh(param->code);
		sm->attr[a].bytes = param->length;

		switch (sm->attr[a].id) {
		case START_STOP_CODE_DATE_MFG:
			sm->attr[a].description = "Date of Manufacture";
			sm->attr[a].flags = SMART_ATTR_F_STR;
			break;
		case START_STOP_CODE_DATE_ACCTN:
			sm->attr[a].description = "Accounting Date";
			sm->attr[a].flags = SMART_ATTR_F_STR;
			break;
		case START_STOP_CODE_CYCLES_LIFE:
			sm->attr[a].description = "Specified Cycle Count Over Device Lifetime";
			sm->attr[a].flags = SMART_ATTR_F_BE;
			break;
		case START_STOP_CODE_CYCLES_ACCUM:
			sm->attr[a].description = "Accumulated Start-Stop Cycles";
			sm->attr[a].flags = SMART_ATTR_F_BE;
			break;
		case START_STOP_CODE_LOAD_LIFE:
			sm->attr[a].description = "Specified Load-Unload Count Over Device Lifetime";
			sm->attr[a].flags = SMART_ATTR_F_BE;
			break;
		case START_STOP_CODE_LOAD_ACCUM:
			sm->attr[a].description = "Accumulated Load-Unload Cycles";
			sm->attr[a].flags = SMART_ATTR_F_BE;
			break;
		}

		sm->attr[a].raw = param->data;
		sm->attr[a].thresh = NULL;

		p += 4 + param->length;

		a++;
		if (a >= sm->sb->attr_count) {
			break;
		}
	}

	sm->count = a;
}

static void
__smart_map_scsi_info_exception(smart_map_t *sm, void *b, size_t bsize)
{
	struct scsi_info_exception_log_page {
		uint8_t page_code;
		uint8_t subpage_code;
		uint16_t page_length;
		uint8_t param[];
	} __attribute__((packed)) *ie = b;
	struct scsi_ie_param {
		uint16_t code;
		uint8_t control;
		uint8_t length;
		uint8_t asc;	/* IE Additional Sense Code */
		uint8_t ascq;	/* IE Additional Sense Code Qualifier */
		uint8_t temp_recent;
		uint8_t temp_trip_point;
		uint8_t temp_max;
	} __attribute__((packed)) *param;
	uint32_t a, p, page_length;

	a = sm->count;

	p = 0;
	page_length = be16toh(ie->page_length);

	if (page_length + 4 > bsize) {
		return;
	}

	while (p < page_length) {
		if (a + 5 > sm->sb->attr_count) {
			break;
		}

		if (p + sizeof(struct scsi_ie_param) > page_length + 4) {
			break;
		}

		param = (struct scsi_ie_param *)(ie->param + p);

		sm->attr[a].page = ie->page_code;
		sm->attr[a].id = offsetof(struct scsi_ie_param, asc);
		sm->attr[a].description = "Informational Exception ASC";
		sm->attr[a].bytes = 1;
		sm->attr[a].flags = 0;
		sm->attr[a].raw = &param->asc;
		sm->attr[a].thresh = NULL;
		a++;

		sm->attr[a].page = ie->page_code;
		sm->attr[a].id = offsetof(struct scsi_ie_param, ascq);
		sm->attr[a].description = "Informational Exception ASCQ";
		sm->attr[a].bytes = 1;
		sm->attr[a].flags = 0;
		sm->attr[a].raw = &param->ascq;
		sm->attr[a].thresh = NULL;
		a++;

		sm->attr[a].page = ie->page_code;
		sm->attr[a].id = offsetof(struct scsi_ie_param, temp_recent);
		sm->attr[a].description = "Informational Exception Most recent temperature";
		sm->attr[a].bytes = 1;
		sm->attr[a].flags = 0;
		sm->attr[a].raw = &param->temp_recent;
		sm->attr[a].thresh = NULL;
		a++;

		sm->attr[a].page = ie->page_code;
		sm->attr[a].id = offsetof(struct scsi_ie_param, temp_trip_point);
		sm->attr[a].description = "Informational Exception Vendor HDA temperature trip point";
		sm->attr[a].bytes = 1;
		sm->attr[a].flags = 0;
		sm->attr[a].raw = &param->temp_trip_point;
		sm->attr[a].thresh = NULL;
		a++;

		sm->attr[a].page = ie->page_code;
		sm->attr[a].id = offsetof(struct scsi_ie_param, temp_max);
		sm->attr[a].description = "Informational Exception Maximum temperature";
		sm->attr[a].bytes = 1;
		sm->attr[a].flags = 0;
		sm->attr[a].raw = &param->temp_max;
		sm->attr[a].thresh = NULL;
		a++;

		p += 4 + param->length;
	}

	sm->count = a;
}

static void
__smart_map_scsi_raw(smart_map_t *sm, void *b, size_t bsize, uint32_t page)
{
	uint32_t a = sm->count;

	sm->attr[a].page = page;
	sm->attr[a].id = 0;
	sm->attr[a].description = "SCSI Log Page";
	sm->attr[a].bytes = bsize;
	sm->attr[a].flags = SMART_ATTR_F_BE;
	sm->attr[a].raw = b;
	sm->attr[a].thresh = NULL;

	sm->count = a + 1;
}

static void
__smart_map_scsi_self_test(smart_map_t *sm, void *b, size_t bsize)
{
	uint32_t a = sm->count;
	uint8_t *buf = b;
	uint16_t page_length;
	uint32_t i, max_entries;

	if (bsize < 4)
		return;

	page_length = (uint16_t)buf[2] << 8 | buf[3];
	if (page_length + 4 > bsize)
		return;

	max_entries = page_length / 20;
	if (max_entries > 20)
		max_entries = 20;

	for (i = 0; i < max_entries && a < sm->sb->attr_count; i++) {
		uint8_t *entry = buf + 4 + (i * 20);
		uint8_t result = entry[4] & 0x0F;

		if (result == 0x0F)
			continue;

		sm->attr[a].page = PAGE_ID_SCSI_SELF_TEST;
		sm->attr[a].id = (uint16_t)entry[0] << 8 | entry[1];
		sm->attr[a].description = "SCSI Self-Test Result";
		sm->attr[a].bytes = 20;
		sm->attr[a].flags = SMART_ATTR_F_BE;
		sm->attr[a].raw = entry;
		sm->attr[a].thresh = NULL;
		a++;
	}

	sm->count = a;
}

/*
 * Create a map based on the page list
 */
static void
__smart_map_scsi(smart_h h, smart_buf_t *sb, smart_map_t *sm)
{
	smart_t *s = h;
	smart_page_list_t *pg_list = NULL;
	uint8_t *b = NULL;
	uint32_t p;

	pg_list = s->pg_list;
	b = sb->b;

	sm->count = 0;

	for (p = 0; p < pg_list->pg_count; p++) {
		switch (pg_list->pages[p].id) {
		case PAGE_ID_SCSI_WRITE_ERR:
		case PAGE_ID_SCSI_READ_ERR:
		case PAGE_ID_SCSI_VERIFY_ERR:
		case PAGE_ID_SCSI_NON_MEDIUM_ERR:
			__smart_map_scsi_err_page(sm, b, pg_list->pages[p].bytes);
			break;
		case PAGE_ID_SCSI_LAST_N_ERR:
			__smart_map_scsi_last_err(sm, b, pg_list->pages[p].bytes);
			break;
		case PAGE_ID_SCSI_TEMPERATURE:
			__smart_map_scsi_temp(sm, b, pg_list->pages[p].bytes);
			break;
		case PAGE_ID_SCSI_START_STOP_CYCLE:
			__smart_map_scsi_start_stop(sm, b, pg_list->pages[p].bytes);
			break;
		case PAGE_ID_SCSI_INFO_EXCEPTION:
			__smart_map_scsi_info_exception(sm, b, pg_list->pages[p].bytes);
			break;
		case PAGE_ID_SCSI_SELF_TEST:
			__smart_map_scsi_self_test(sm, b, pg_list->pages[p].bytes);
			break;
		case PAGE_ID_SCSI_SS_MEDIA:
		case PAGE_ID_SCSI_BG_SCAN:
		case PAGE_ID_SCSI_PROTO_SPECIFIC:
			__smart_map_scsi_raw(sm, b, pg_list->pages[p].bytes,
			    pg_list->pages[p].id);
			break;
		}

		b += pg_list->pages[p].bytes;
	}
}

/**
 * Create a map of SMART values
 */
static void
__smart_attribute_map(smart_h h, smart_buf_t *sb, smart_map_t *sm)
{

	if (!sb || !sm) {
		return;
	}

	switch (sb->protocol) {
	case SMART_PROTO_ATA:
		__smart_map_ata(h, sb, sm);
		break;
	case SMART_PROTO_NVME:
		__smart_map_nvme(sb, sm);
		break;
	case SMART_PROTO_SCSI:
		__smart_map_scsi(h, sb, sm);
		break;
	default:
		sm->count = 0;
	}
}

static smart_map_t *
__smart_map(smart_h h, smart_buf_t *sb)
{
	smart_map_t *sm = NULL;
	uint32_t max = 0;

	max = sb->attr_count;
	if (max == 0) {
		warnx("Attribute count is zero?!?");
		return NULL;
	}

	sm = malloc(sizeof(smart_map_t) + (max * sizeof(smart_attr_t)));
	if (sm) {
		memset(sm, 0, sizeof(smart_map_t) + (max * sizeof(smart_attr_t)));
		sm->sb = sb;

		/* count starts as the max but is adjusted to reflect the actual number */
		sm->count = max;

		__smart_attribute_map(h, sb, sm);
	}

	return sm;
}

typedef struct {
	uint8_t	page_code;
	uint8_t	subpage_code;
	uint16_t page_length;
	uint8_t supported_pages[];
} __attribute__((packed)) scsi_supported_log_pages;

static smart_page_list_t *
__smart_page_list_scsi(smart_t *s)
{
	smart_page_list_t *pg_list = NULL;
	scsi_supported_log_pages *b = NULL;
	uint32_t bsize = 256;	/* 4 byte header + 252 entries, matching smartmontools LOG_RESP_LEN */
	int32_t rc;

	b = malloc(bsize);
	if (!b) {
		return NULL;
	}

	/* Supported Pages page ID is 0 */
	rc = device_read_log(s, PAGE_ID_SCSI_SUPPORTED_PAGES, (uint8_t *)b,
			bsize);
	if (rc < 0) {
		dprintf("Read Supported Log Pages failed\n");
	} else {
		uint8_t *supported_page = b->supported_pages;
		uint32_t n_supported = be16toh(b->page_length);
		uint32_t s, p, pmax = pg_list_scsi.pg_count;

		if (n_supported > bsize - 4) {
			n_supported = bsize - 4;
		}

		/* Build a page list using only pages the device supports */
		pg_list = malloc(sizeof(smart_page_list_t) +
		    pg_list_scsi.pg_count * sizeof(pg_list_scsi.pages[0]));
		if (pg_list == NULL) {
			n_supported = 0;
		} else {
			pg_list->pg_count = 0;
		}

		/*
		 * Loop through all supported pages looking for those related
		 * to SMART. The below assumes the supported page list from the
		 * device and in pg_lsit_scsi are sorted in increasing order.
		 */
		dprintf("Supported SCSI pages:\n");
		for (s = 0, p = 0; (s < n_supported) && (p < pmax); s++) {
			dprintf("\t[%u] = %#x\n", s, supported_page[s]);
			while ((supported_page[s] > pg_list_scsi.pages[p].id) &&
					(p < pmax)) {
				p++;
			}

			if (supported_page[s] == pg_list_scsi.pages[p].id) {
				pg_list->pages[pg_list->pg_count] = pg_list_scsi.pages[p];
				pg_list->pg_count++;
				p++;
			}
		}
	}

	free(b);

	return pg_list;
}

static smart_page_list_t *
__smart_page_list(smart_h h)
{
	smart_t *s = h;
	smart_page_list_t *pg_list = NULL;

	if (!s) {
		return NULL;
	}

	switch (s->protocol) {
	case SMART_PROTO_ATA:
		pg_list = &pg_list_ata;
		break;
	case SMART_PROTO_NVME:
		pg_list = &pg_list_nvme;
		break;
	case SMART_PROTO_SCSI:
		pg_list = __smart_page_list_scsi(s);
		break;
	default:
		pg_list = NULL;
	}

	return pg_list;
}

static int32_t
__smart_read_pages(smart_h h, smart_buf_t *sb)
{
	smart_t *s = h;
	smart_page_list_t *plist = NULL;
	uint8_t *buf = NULL;
	int32_t rc = 0;
	uint32_t p = 0;

	plist = s->pg_list;

	buf = sb->b;

	/* Zero the entire buffer so unread pages contain safe zeroes */
	bzero(buf, sb->bsize);

	for (p = 0; p < s->pg_list->pg_count; p++) {
		rc = device_read_log(h, plist->pages[p].id, buf, plist->pages[p].bytes);
		if (rc) {
			dprintf("bad read (%d) from page %#x (bytes=%lu)\n", rc,
					plist->pages[p].id, plist->pages[p].bytes);
			break; 
		}

		buf += plist->pages[p].bytes;
	}

	return rc;
}
