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
#include <fcntl.h>
#include <string.h>
#include <err.h>
#include <errno.h>
#include <camlib.h>
#include <cam/cam.h>
#include <cam/scsi/scsi_message.h>

#include "libsmart.h"
#include "libsmart_priv.h"

/* Provide compatibility for FreeBSD 11.0 */
#if (__FreeBSD_version < 1101000)

struct scsi_log_informational_exceptions {
        struct scsi_log_param_header hdr;
#define SLP_IE_GEN                      0x0000
        uint8_t ie_asc;
        uint8_t ie_ascq;
        uint8_t temperature;
};

#endif

struct fbsd_smart {
	smart_t	common;
	struct cam_device *camdev;
	int	last_cam_err;	/* per-handle, no thread-safety issue */
	bool	read_only;
};

/*
 * Retrieve and clear the last CAM device error for this handle.
 * Returns 0 if no error occurred since the last call.
 */
int smart_get_last_err(smart_h h) {
	struct fbsd_smart *fs = (struct fbsd_smart *)h;
	if (!fs)
		return 0;
	int e = fs->last_cam_err;
	fs->last_cam_err = 0;
	return e;
}

static smart_protocol_e __device_get_proto(struct fbsd_smart *);
static bool __device_proto_tunneled(struct fbsd_smart *);
static int32_t __device_get_info(struct fbsd_smart *);

smart_h
device_open(smart_protocol_e protocol, char *devname)
{
	struct fbsd_smart *h = NULL;

	h = malloc(sizeof(struct fbsd_smart));
	if (h == NULL)
		return NULL;

	memset(h, 0, sizeof(struct fbsd_smart));

	h->common.protocol = SMART_PROTO_MAX;
	h->camdev = cam_open_device(devname, O_RDWR);
	if (h->camdev == NULL) {
		h->camdev = cam_open_device(devname, O_RDONLY);
		h->read_only = h->camdev != NULL;
	}
	if (h->camdev == NULL) {
		fprintf(stderr, "%s: error opening %s - %s\n",
		    __func__, devname, cam_errbuf);
		free(h);
		return NULL;
	}

	smart_protocol_e proto = __device_get_proto(h);
	if (proto == SMART_PROTO_MAX)
		goto device_open_fail;
	h->common.protocol = proto;

	if (proto == SMART_PROTO_SCSI && __device_proto_tunneled(h)) {
		h->common.protocol = SMART_PROTO_ATA;
		h->common.info.tunneled = 1;
	}
	if (protocol != SMART_PROTO_AUTO && protocol != h->common.protocol) {
		dprintf("%s: protocol mismatch %d vs %d\n",
		    __func__, protocol, h->common.protocol);
		goto device_open_fail;
	}

	if (__device_get_info(h) != 0)
		goto device_open_fail;

	return h;

device_open_fail:
	cam_close_device(h->camdev);
	free(h);
	return NULL;
}

void
device_close(smart_h h)
{
	struct fbsd_smart *fsmart = h;

	if (fsmart != NULL) {
		if (fsmart->camdev != NULL) {
			cam_close_device(fsmart->camdev);
		}

		if (fsmart->common.pg_list != NULL &&
		    fsmart->common.protocol == SMART_PROTO_SCSI) {
			free(fsmart->common.pg_list);
		}

		free(fsmart);
	}
}

static const uint8_t smart_read_data[] = {
	0xb0, 0xd0, 0x00, 0x4f, 0xc2, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00
};

static const uint8_t smart_read_thresholds[] = {
	0xb0, 0xd1, 0x00, 0x4f, 0xc2, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00
};

static const uint8_t smart_return_status[] = {
	0xb0, 0xda, 0x00, 0x4f, 0xc2, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00
};

static int32_t
__device_read_ata(smart_h h, uint32_t page, void *buf, size_t bsize, union ccb *ccb)
{
	struct fbsd_smart *fsmart = h;
	const uint8_t *smart_fis;
	uint32_t smart_fis_size = 0;
	uint32_t cam_flags = 0;
	uint16_t sector_count = 0;
	uint8_t protocol = 0;

	switch (page) {
	case PAGE_ID_ATA_SMART_READ_DATA: /* Support SMART READ DATA */
		smart_fis = smart_read_data;
		smart_fis_size = sizeof(smart_read_data);
		cam_flags = CAM_DIR_IN;
		sector_count = 1;
		protocol = AP_PROTO_PIO_IN;
		break;
	case PAGE_ID_ATA_SMART_READ_THRESHOLDS: /* Support SMART READ THRESHOLDS */
		smart_fis = smart_read_thresholds;
		smart_fis_size = sizeof(smart_read_thresholds);
		cam_flags = CAM_DIR_IN;
		sector_count = 1;
		protocol = AP_PROTO_PIO_IN;
		break;
	case PAGE_ID_ATA_SMART_RET_STATUS: /* Support SMART RETURN STATUS */
		smart_fis = smart_return_status;
		smart_fis_size = sizeof(smart_return_status);
		/* Command has no data but uses the return status */
		cam_flags = CAM_DIR_NONE;
		protocol = AP_PROTO_NON_DATA;
		bsize = 0;
		break;
	default:
		return EINVAL;
	}

	if (fsmart->common.info.tunneled) {
		struct ata_pass_16 *cdb;
		uint8_t cdb_flags;

		if (bsize > 0) {
			cdb_flags = AP_FLAG_TDIR_FROM_DEV |
				AP_FLAG_BYT_BLOK_BLOCKS |
				AP_FLAG_TLEN_SECT_CNT;
		} else {
			cdb_flags = AP_FLAG_CHK_COND |
				AP_FLAG_TDIR_FROM_DEV |
				AP_FLAG_BYT_BLOK_BLOCKS;
		}

		cdb = (struct ata_pass_16 *)ccb->csio.cdb_io.cdb_bytes;
		bzero(cdb, sizeof(*cdb));

		scsi_ata_pass_16(&ccb->csio,
				/*retries*/	1,
				/*cbfcnp*/	NULL,
				/*flags*/	cam_flags,
				/*tag_action*/	MSG_SIMPLE_Q_TAG,
				/*protocol*/	protocol,
				/*ata_flags*/	cdb_flags,
				/*features*/	page,
				/*sector_count*/sector_count,
				/*lba*/		0,
				/*command*/	ATA_SMART_CMD,
				/*control*/	0,
				/*data_ptr*/	buf,
				/*dxfer_len*/	bsize,
				/*sense_len*/	SSD_FULL_SIZE,
			30000
				);
		cdb->lba_mid = 0x4f;
		cdb->lba_high = 0xc2;
		cdb->device = 0;	/* scsi_ata_pass_16() sets this */
	} else {
		bcopy(smart_fis, &ccb->ataio.cmd.command, smart_fis_size);

		cam_fill_ataio(&ccb->ataio,
				0,
				/* cbfcnp */NULL,
				/* flags */cam_flags,
				MSG_SIMPLE_Q_TAG,
				/* data_ptr */buf,
				/* dxfer_len */bsize,
			30000);
		ccb->ataio.cmd.flags |= CAM_ATAIO_NEEDRESULT;
		ccb->ataio.cmd.control = 0;
	}

	return 0;
}

static int32_t __device_read_scsi(smart_h, uint32_t, uint8_t, void *, size_t, union ccb *);

static int32_t
__device_scsi_log_sense(struct fbsd_smart *fsmart, uint32_t page, void *buf, size_t bsize)
{
	union ccb *ccb = NULL;
	int rc = 0;
	uint8_t *b = buf;

	if (fsmart == NULL || buf == NULL || bsize < 4)
		return EINVAL;

	fsmart->last_cam_err = 0;
	dprintf("read log page %#x\n", page);
	bzero(buf, bsize);

	ccb = cam_getccb(fsmart->camdev);
	if (ccb == NULL)
		return ENOMEM;

	CCB_CLEAR_ALL_EXCEPT_HDR(ccb);
	__device_read_scsi((smart_h)fsmart, page, 0, buf, bsize, ccb);

	ccb->ccb_h.flags |= CAM_DEV_QFRZDIS;
	rc = cam_send_ccb(fsmart->camdev, ccb);
	if (rc < 0 || (ccb->ccb_h.status & CAM_STATUS_MASK) != CAM_REQ_CMP) {
		if (rc < 0) {
			rc = errno ? errno : EIO;
		} else {
			uint32_t cam_status = ccb->ccb_h.status & CAM_STATUS_MASK;
			switch (cam_status) {
			case CAM_CMD_TIMEOUT:
				rc = ETIMEDOUT;
				break;
			case CAM_REQ_ABORTED:
			case CAM_SCSI_BUS_RESET:
			case CAM_SEQUENCE_FAIL:
				rc = ECONNABORTED;
				break;
			default:
				rc = EIO;
				break;
			}
		}
		if (rc != 0)
			fsmart->last_cam_err = rc;
		cam_freeccb(ccb);
		return rc;
	}

	if (page != 0 && (b[0] & 0x3f) != page) {
		cam_freeccb(ccb);
		return EIO;
	}

	cam_freeccb(ccb);
	return 0;
}

static int32_t
__device_read_scsi(smart_h h, uint32_t page, uint8_t subpage, void *buf, size_t bsize, union ccb *ccb)
{
	(void)h;

	scsi_log_sense(&ccb->csio,
			/* retries */1,
			/* cbfcnp */NULL,
			/* tag_action */0,
			/* page_code */SLS_PAGE_CTRL_CUMULATIVE,
			/* page */page,
			/* save_pages */0,
			/* ppc */0,
			/* paramptr */0,
			/* param_buf */buf,
			/* param_len */bsize,
			/* sense_len */0,
			30000);

	if (subpage > 0)
		ccb->csio.cdb_io.cdb_bytes[3] = subpage;

	return 0;
}

static int32_t
__device_read_nvme(smart_h h, uint32_t page, void *buf, size_t bsize, union ccb *ccb)
{
	(void)h;
	struct ccb_nvmeio *nvmeio = &ccb->nvmeio;
	uint32_t numd = 0;	/* number of dwords */

	/*
	 * NVME CAM passthru
	 *    1200000 > version > 1101510 uses nvmeio->cmd.opc
	 *    1200059 > version > 1200038 uses nvmeio->cmd.opc
	 *    1200081 > version > 1200058 uses nvmeio->cmd.opc_fuse
	 *                      > 1200080 uses nvmeio->cmd.opc
	 * This code doesn't support the brief 'opc_fuse' period.
	 */
#if ((__FreeBSD_version > 1200038) || ((__FreeBSD_version > 1101510) && (__FreeBSD_version < 1200000)))
	switch (page) {
	case NVME_LOG_HEALTH_INFORMATION:
		numd = (sizeof(struct nvme_health_information_page) / sizeof(uint32_t));
		break;
	default:
		/* Unsupported log page */
		return EINVAL;
	}

	/* Subtract 1 because NUMD is a zero based value */
	numd--;

	nvmeio->cmd.opc = NVME_OPC_GET_LOG_PAGE;
	nvmeio->cmd.nsid = NVME_GLOBAL_NAMESPACE_TAG;
	nvmeio->cmd.cdw10 = page | (numd << 16);

	cam_fill_nvmeadmin(&ccb->nvmeio,
			/* retries */1,
			/* cbfcnp */NULL,
			/* flags */CAM_DIR_IN,
			/* data_ptr */buf,
			/* dxfer_len */bsize,
			30000);
#endif
	return 0;
}

/*
 * Retrieve the SMART RETURN STATUS
 *
 * SMART RETURN STATUS provides the reliability status of the
 * device and can be used as a high-level indication of health.
 */
static int32_t
__device_status_ata(smart_h h, union ccb *ccb)
{
	struct fbsd_smart *fsmart = h;
	uint8_t *buf = NULL;
	uint32_t page = 0;
	uint8_t lba_high = 0, lba_mid = 0, device = 0, status = 0;

	if (fsmart->common.info.tunneled) {
		struct ata_res_pass16 {
			u_int16_t reserved[5];
			u_int8_t flags;
			u_int8_t error;
			u_int8_t sector_count_exp;
			u_int8_t sector_count;
			u_int8_t lba_low_exp;
			u_int8_t lba_low;
			u_int8_t lba_mid_exp;
			u_int8_t lba_mid;
			u_int8_t lba_high_exp;
			u_int8_t lba_high;
			u_int8_t device;
			u_int8_t status;
		} *res_pass16 = (struct ata_res_pass16 *)(uintptr_t)
			    &ccb->csio.sense_data;

		buf = ccb->csio.data_ptr;
		page = ((struct ata_pass_16 *)ccb->csio.cdb_io.cdb_bytes)->features;
		lba_high = res_pass16->lba_high;
		lba_mid = res_pass16->lba_mid;
		device = res_pass16->device;
		status = res_pass16->status;

		/*
		 * Note that this generates an expected CHECK CONDITION.
		 * Mask it so the outer function doesn't print an error
		 * message.
		 */
		ccb->ccb_h.status &= ~CAM_STATUS_MASK;
		ccb->ccb_h.status |= CAM_REQ_CMP;
	} else {
		struct ccb_ataio *ataio = (struct ccb_ataio *)&ccb->ataio;

		buf = ataio->data_ptr;
		page = ataio->cmd.features;
		lba_high = ataio->res.lba_high;
		lba_mid = ataio->res.lba_mid;
		device = ataio->res.device;
		status = ataio->res.status;
	}

	switch (page) {
	case PAGE_ID_ATA_SMART_RET_STATUS:
		/*
		 * Typically, SMART related log pages return data, but this
		 * command is different in that the data is encoded in the
		 * result registers.
		 *
		 * Handle this in a UNIX-like way by writing a 0 (no errors)
		 * or 1 (threshold exceeded condition) to the output buffer.
		 */
		dprintf("SMART_RET_STATUS: lba mid=%#x high=%#x device=%#x status=%#x\n",
				lba_mid,
				lba_high,
				device,
				status);
		if ((lba_high == 0x2c) && (lba_mid == 0xf4)) {
			buf[0] = 1;
		} else if ((lba_high == 0xc2) && (lba_mid == 0x4f)) {
			buf[0] = 0;
		} else {
			/* Ruh-roh ... */
			buf[0] = 255;
		}
		break;
	default:
		;
	}

	return 0;
}

int32_t
device_self_test(smart_h h, uint8_t test_type)
{
	struct fbsd_smart *fsmart = h;
	union ccb *ccb = NULL;
	uint8_t smart_fis[12];
	int rc = 0;

	if (fsmart == NULL)
		return EINVAL;
	if (fsmart->read_only)
		return EROFS;

	fsmart->last_cam_err = 0;
	ccb = cam_getccb(fsmart->camdev);
	if (ccb == NULL)
		return ENOMEM;

	CCB_CLEAR_ALL_EXCEPT_HDR(ccb);

	switch (fsmart->common.protocol) {
	case SMART_PROTO_ATA:
		switch (test_type) {
		case ATA_SELF_TEST_OFFLINE:
		case ATA_SELF_TEST_SHORT:
		case ATA_SELF_TEST_EXTENDED:
		case ATA_SELF_TEST_CONVEYANCE:
		case ATA_SELF_TEST_SELECTIVE:
		case ATA_SELF_TEST_ABORT:
		case ATA_SELF_TEST_SHORT_CAPTIVE:
		case ATA_SELF_TEST_EXTENDED_CAPTIVE:
		case ATA_SELF_TEST_CONVEYANCE_CAPTIVE:
		case ATA_SELF_TEST_SELECTIVE_CAPTIVE:
			break;
		default:
			cam_freeccb(ccb);
			return EINVAL;
		}
		if (fsmart->common.info.tunneled) {
			struct ata_pass_16 *cdb;

			cdb = (struct ata_pass_16 *)ccb->csio.cdb_io.cdb_bytes;
			bzero(cdb, sizeof(*cdb));

			scsi_ata_pass_16(&ccb->csio,
					1, NULL,
					CAM_DIR_NONE,
					MSG_SIMPLE_Q_TAG,
					AP_PROTO_NON_DATA,
					AP_FLAG_TDIR_FROM_DEV |
					    AP_FLAG_BYT_BLOK_BLOCKS,
					0xD4,
					0,
					test_type,
					ATA_SMART_CMD,
					0,
					NULL, 0,
					SSD_FULL_SIZE,
					30000);
			cdb->lba_mid = 0x4f;
			cdb->lba_high = 0xc2;
			cdb->device = 0;
		} else {
			memset(smart_fis, 0, sizeof(smart_fis));
			smart_fis[0] = ATA_SMART_CMD;
			smart_fis[1] = 0xD4;
			smart_fis[2] = test_type;
			smart_fis[3] = 0x4f;
			smart_fis[4] = 0xc2;

			bcopy(smart_fis, &ccb->ataio.cmd.command, sizeof(smart_fis));

			cam_fill_ataio(&ccb->ataio,
					0, NULL,
					CAM_DIR_NONE,
					MSG_SIMPLE_Q_TAG,
					NULL, 0,
					30000);
			ccb->ataio.cmd.flags |= CAM_ATAIO_NEEDRESULT;
			ccb->ataio.cmd.control = 0;
		}
		break;

	case SMART_PROTO_SCSI: {
		int self_test = 0;
		int self_test_code;
		uint32_t timeout = 30000;

		switch (test_type) {
		case 0x00:
			self_test = 1;
			self_test_code = SSD_SELF_TEST_CODE_NONE;
			break;
		case ATA_SELF_TEST_SHORT:
			self_test_code = SSD_SELF_TEST_CODE_BG_SHORT;
			break;
		case ATA_SELF_TEST_EXTENDED:
			self_test_code = SSD_SELF_TEST_CODE_BG_EXTENDED;
			break;
		case ATA_SELF_TEST_ABORT:
		case NVME_STC_ABORT:
			self_test_code = SSD_SELF_TEST_CODE_BG_ABORT;
			break;
		case ATA_SELF_TEST_SHORT_CAPTIVE:
			self_test_code = SSD_SELF_TEST_CODE_FG_SHORT;
			timeout = 12U * 60U * 60U * 1000U;
			break;
		case ATA_SELF_TEST_EXTENDED_CAPTIVE:
			self_test_code = SSD_SELF_TEST_CODE_FG_EXTENDED;
			timeout = 12U * 60U * 60U * 1000U;
			break;
		default:
			cam_freeccb(ccb);
			return EINVAL;
		}

		scsi_send_diagnostic(&ccb->csio,
				1, NULL,
				MSG_SIMPLE_Q_TAG,
				0, 0,
				self_test,
				0,
				self_test_code,
				NULL, 0,
				SSD_FULL_SIZE,
				timeout);
		}
		break;

	case SMART_PROTO_NVME: {
		struct ccb_nvmeio *nvmeio = &ccb->nvmeio;
		uint8_t nvme_type = test_type;
		uint32_t nsid = fsmart->common.info.nvme_nsid;
		if (nsid == 0)
			nsid = NVME_GLOBAL_NAMESPACE_TAG;
		if (nvme_type == 0x7F)
			nvme_type = NVME_STC_ABORT;
		if (nvme_type != NVME_STC_SHORT &&
		    nvme_type != NVME_STC_EXTENDED &&
		    nvme_type != NVME_STC_ABORT) {
			cam_freeccb(ccb);
			return EINVAL;
		}
		nvmeio->cmd.opc = NVME_OPC_DEVICE_SELF_TEST;
		nvmeio->cmd.nsid = nsid;
		nvmeio->cmd.cdw10 = nvme_type;

		cam_fill_nvmeadmin(&ccb->nvmeio,
				1, NULL,
				CAM_DIR_NONE,
				NULL, 0,
				5000);
		}
		break;

	default:
		cam_freeccb(ccb);
		return ENODEV;
	}

	ccb->ccb_h.flags |= CAM_DEV_QFRZDIS;

	rc = cam_send_ccb(fsmart->camdev, ccb);
	if (rc < 0) {
		warn("error sending self-test command");
		rc = errno ? errno : EIO;
	} else if ((ccb->ccb_h.status & CAM_STATUS_MASK) != CAM_REQ_CMP) {
		if (do_debug)
			cam_error_print(fsmart->camdev, ccb, CAM_ESF_ALL,
					CAM_EPF_ALL, stderr);
		uint32_t cam_status = ccb->ccb_h.status & CAM_STATUS_MASK;
		switch (cam_status) {
		case CAM_CMD_TIMEOUT:
			rc = ETIMEDOUT;
			break;
		case CAM_REQ_ABORTED:
		case CAM_SCSI_BUS_RESET:
		case CAM_SEQUENCE_FAIL:
			rc = ECONNABORTED;
			break;
		default:
			rc = EIO;
			break;
		}
	}

	if (rc != 0)
		fsmart->last_cam_err = rc;

	cam_freeccb(ccb);
	return rc;
}

int32_t
device_scsi_request_sense(smart_h h, void *buf, size_t bsize)
{
	struct fbsd_smart *fsmart = h;
	union ccb *ccb;
	int rc;

	if (fsmart == NULL || buf == NULL || bsize < 18 || bsize > UINT8_MAX)
		return EINVAL;
	if (fsmart->common.protocol != SMART_PROTO_SCSI)
		return ENODEV;

	fsmart->last_cam_err = 0;
	bzero(buf, bsize);
	ccb = cam_getccb(fsmart->camdev);
	if (ccb == NULL)
		return ENOMEM;
	CCB_CLEAR_ALL_EXCEPT_HDR(ccb);
	scsi_request_sense(&ccb->csio, 1, NULL, buf, (uint8_t)bsize,
	    MSG_SIMPLE_Q_TAG, SSD_FULL_SIZE, 30000);
	ccb->ccb_h.flags |= CAM_DEV_QFRZDIS;
	rc = cam_send_ccb(fsmart->camdev, ccb);
	if (rc < 0)
		rc = errno ? errno : EIO;
	else if ((ccb->ccb_h.status & CAM_STATUS_MASK) != CAM_REQ_CMP) {
		switch (ccb->ccb_h.status & CAM_STATUS_MASK) {
		case CAM_CMD_TIMEOUT:
			rc = ETIMEDOUT;
			break;
		case CAM_REQ_ABORTED:
		case CAM_SCSI_BUS_RESET:
		case CAM_SEQUENCE_FAIL:
			rc = ECONNABORTED;
			break;
		default:
			rc = EIO;
			break;
		}
	} else
		rc = 0;
	if (rc != 0)
		fsmart->last_cam_err = rc;
	cam_freeccb(ccb);
	return rc;
}

int32_t
device_scsi_control_mode_page(smart_h h, void *buf, size_t bsize)
{
	struct fbsd_smart *fsmart = h;
	const int command_sizes[] = { 6, 10 };
	int rc = EIO;
	size_t i;

	if (fsmart == NULL || buf == NULL || bsize < 20 || bsize > UINT32_MAX)
		return EINVAL;
	if (fsmart->common.protocol != SMART_PROTO_SCSI)
		return ENODEV;

	fsmart->last_cam_err = 0;
	for (i = 0; i < sizeof(command_sizes) / sizeof(command_sizes[0]); i++) {
		union ccb *ccb;

		bzero(buf, bsize);
		ccb = cam_getccb(fsmart->camdev);
		if (ccb == NULL)
			return ENOMEM;
		CCB_CLEAR_ALL_EXCEPT_HDR(ccb);
		scsi_mode_sense_len(&ccb->csio, 1, NULL, MSG_SIMPLE_Q_TAG, 1,
		    SMS_PAGE_CTRL_CURRENT, SMS_CONTROL_MODE_PAGE, buf,
		    (uint32_t)bsize, command_sizes[i], SSD_FULL_SIZE, 30000);
		ccb->ccb_h.flags |= CAM_DEV_QFRZDIS;
		rc = cam_send_ccb(fsmart->camdev, ccb);
		if (rc < 0)
			rc = errno ? errno : EIO;
		else if ((ccb->ccb_h.status & CAM_STATUS_MASK) == CAM_REQ_CMP)
			rc = 0;
		else {
			switch (ccb->ccb_h.status & CAM_STATUS_MASK) {
			case CAM_CMD_TIMEOUT:
				rc = ETIMEDOUT;
				break;
			case CAM_REQ_ABORTED:
			case CAM_SCSI_BUS_RESET:
			case CAM_SEQUENCE_FAIL:
				rc = ECONNABORTED;
				break;
			default:
				rc = EIO;
				break;
			}
		}
		cam_freeccb(ccb);
		if (rc == 0)
			return 0;
		if (rc == ETIMEDOUT || rc == ECONNABORTED)
			break;
	}
	fsmart->last_cam_err = rc;
	return rc;
}

int32_t
device_scsi_extended_inquiry(smart_h h, void *buf, size_t bsize)
{
	struct fbsd_smart *fsmart = h;
	union ccb *ccb;
	int rc;

	if (fsmart == NULL || buf == NULL || bsize < 12 || bsize > UINT32_MAX)
		return EINVAL;
	if (fsmart->common.protocol != SMART_PROTO_SCSI)
		return ENODEV;

	fsmart->last_cam_err = 0;
	bzero(buf, bsize);
	ccb = cam_getccb(fsmart->camdev);
	if (ccb == NULL)
		return ENOMEM;
	CCB_CLEAR_ALL_EXCEPT_HDR(ccb);
	scsi_inquiry(&ccb->csio, 1, NULL, MSG_SIMPLE_Q_TAG, buf,
	    (uint32_t)bsize, 1, SVPD_EXTENDED_INQUIRY_DATA, SSD_FULL_SIZE,
	    30000);
	ccb->ccb_h.flags |= CAM_DEV_QFRZDIS;
	rc = cam_send_ccb(fsmart->camdev, ccb);
	if (rc < 0)
		rc = errno ? errno : EIO;
	else if ((ccb->ccb_h.status & CAM_STATUS_MASK) != CAM_REQ_CMP) {
		switch (ccb->ccb_h.status & CAM_STATUS_MASK) {
		case CAM_CMD_TIMEOUT:
			rc = ETIMEDOUT;
			break;
		case CAM_REQ_ABORTED:
		case CAM_SCSI_BUS_RESET:
		case CAM_SEQUENCE_FAIL:
			rc = ECONNABORTED;
			break;
		default:
			rc = EIO;
			break;
		}
	} else
		rc = 0;
	if (rc != 0)
		fsmart->last_cam_err = rc;
	cam_freeccb(ccb);
	return rc;
}

int32_t
device_read_smart_log(smart_h h, uint8_t log_addr, void *buf, size_t bsize)
{
	struct fbsd_smart *fsmart = h;
	union ccb *ccb = NULL;
	uint8_t smart_fis[12];
	int rc = 0;

	if (fsmart == NULL || buf == NULL || bsize == 0)
		return EINVAL;
	if (fsmart->common.protocol == SMART_PROTO_ATA && bsize != 512)
		return EINVAL;
	if (fsmart->common.protocol == SMART_PROTO_NVME && (bsize % 4) != 0)
		return EINVAL;

	fsmart->last_cam_err = 0;
	ccb = cam_getccb(fsmart->camdev);
	if (ccb == NULL)
		return ENOMEM;

	CCB_CLEAR_ALL_EXCEPT_HDR(ccb);

	switch (fsmart->common.protocol) {
	case SMART_PROTO_ATA:
		if (fsmart->common.info.tunneled) {
			struct ata_pass_16 *cdb;

			cdb = (struct ata_pass_16 *)ccb->csio.cdb_io.cdb_bytes;
			bzero(cdb, sizeof(*cdb));

			scsi_ata_pass_16(&ccb->csio,
					1, NULL,
					CAM_DIR_IN,
					MSG_SIMPLE_Q_TAG,
					AP_PROTO_PIO_IN,
					AP_FLAG_TDIR_FROM_DEV |
					    AP_FLAG_BYT_BLOK_BLOCKS |
					    AP_FLAG_TLEN_SECT_CNT,
					0xD5,
					1,
					log_addr,
					ATA_SMART_CMD,
					0,
					buf, bsize,
					SSD_FULL_SIZE,
					30000);
			cdb->lba_mid = 0x4f;
			cdb->lba_high = 0xc2;
			cdb->device = 0;
		} else {
			memset(smart_fis, 0, sizeof(smart_fis));
			smart_fis[0] = ATA_SMART_CMD;
			smart_fis[1] = 0xD5;
			smart_fis[2] = log_addr;
			smart_fis[3] = 0x4f;
			smart_fis[4] = 0xc2;
			smart_fis[10] = 1;

			bcopy(smart_fis, &ccb->ataio.cmd.command, sizeof(smart_fis));

			cam_fill_ataio(&ccb->ataio,
					0, NULL,
					CAM_DIR_IN,
					MSG_SIMPLE_Q_TAG,
					buf, bsize,
					30000);
			ccb->ataio.cmd.flags |= CAM_ATAIO_NEEDRESULT;
			ccb->ataio.cmd.control = 0;
		}
		break;

	case SMART_PROTO_NVME: {
		struct ccb_nvmeio *nvmeio = &ccb->nvmeio;
		uint32_t numd = (uint32_t)(bsize / sizeof(uint32_t));
		if (numd > 0)
			numd--;
		nvmeio->cmd.opc = NVME_OPC_GET_LOG_PAGE;
		nvmeio->cmd.nsid = NVME_GLOBAL_NAMESPACE_TAG;
		nvmeio->cmd.cdw10 = log_addr | (numd << 16);

		cam_fill_nvmeadmin(&ccb->nvmeio,
				1, NULL,
				CAM_DIR_IN,
				buf, bsize,
				5000);
		}
		break;

	default:
		cam_freeccb(ccb);
		return ENODEV;
	}

	ccb->ccb_h.flags |= CAM_DEV_QFRZDIS;

	rc = cam_send_ccb(fsmart->camdev, ccb);
	if (rc < 0) {
		warn("error sending read log command");
		rc = errno ? errno : EIO;
	} else if ((ccb->ccb_h.status & CAM_STATUS_MASK) != CAM_REQ_CMP) {
		if (do_debug)
			cam_error_print(fsmart->camdev, ccb, CAM_ESF_ALL,
					CAM_EPF_ALL, stderr);
		uint32_t cam_status = ccb->ccb_h.status & CAM_STATUS_MASK;
		switch (cam_status) {
		case CAM_CMD_TIMEOUT:
			rc = ETIMEDOUT;
			break;
		case CAM_REQ_ABORTED:
		case CAM_SCSI_BUS_RESET:
		case CAM_SEQUENCE_FAIL:
			rc = ECONNABORTED;
			break;
		default:
			rc = EIO;
			break;
		}
	}

	if (rc != 0)
		fsmart->last_cam_err = rc;

	cam_freeccb(ccb);
	return rc;
}

int32_t
device_read_log_ext(smart_h h, uint8_t logaddr, uint16_t page, void *buf, size_t bsize)
{
	struct fbsd_smart *fsmart = h;
	union ccb *ccb = NULL;
	uint16_t sector_count;
	int rc = 0;

	if (fsmart == NULL || buf == NULL || bsize == 0 || (bsize % 512) != 0)
		return EINVAL;
	if ((bsize / 512) > UINT16_MAX)
		return EOVERFLOW;

	sector_count = (uint16_t)(bsize / 512);

	fsmart->last_cam_err = 0;
	ccb = cam_getccb(fsmart->camdev);
	if (ccb == NULL)
		return ENOMEM;

	CCB_CLEAR_ALL_EXCEPT_HDR(ccb);

	if (fsmart->common.protocol != SMART_PROTO_ATA) {
		cam_freeccb(ccb);
		return ENODEV;
	}

	if (fsmart->common.info.tunneled) {
		struct ata_pass_16 *cdb;

		cdb = (struct ata_pass_16 *)ccb->csio.cdb_io.cdb_bytes;
		bzero(cdb, sizeof(*cdb));

		scsi_ata_pass_16(&ccb->csio,
				1, NULL,
				CAM_DIR_IN,
				MSG_SIMPLE_Q_TAG,
				AP_PROTO_PIO_IN,
				AP_FLAG_TDIR_FROM_DEV |
				    AP_FLAG_BYT_BLOK_BLOCKS |
				    AP_FLAG_TLEN_SECT_CNT,
				0,
				sector_count,
				0,
				0x2F,
				0,
				buf, bsize,
					SSD_FULL_SIZE,
					30000);
		cdb->protocol |= AP_EXTEND;
		cdb->sector_count_ext = (uint8_t)(sector_count >> 8);
		cdb->lba_low = logaddr;
		cdb->lba_mid = (uint8_t)page;
		cdb->lba_mid_ext = (uint8_t)(page >> 8);
		cdb->lba_high = 0;
		cdb->device = 0x40;
	} else {
		cam_fill_ataio(&ccb->ataio,
				0, NULL,
				CAM_DIR_IN,
				MSG_SIMPLE_Q_TAG,
				buf, bsize,
				30000);
		ccb->ataio.cmd.flags |= CAM_ATAIO_NEEDRESULT | CAM_ATAIO_48BIT;
		ccb->ataio.cmd.command = 0x2F;
		ccb->ataio.cmd.features = 0;
		ccb->ataio.cmd.lba_low = logaddr;
		ccb->ataio.cmd.lba_mid = (uint8_t)page;
		ccb->ataio.cmd.lba_mid_exp = (uint8_t)(page >> 8);
		ccb->ataio.cmd.lba_high = 0;
		ccb->ataio.cmd.lba_high_exp = 0;
		ccb->ataio.cmd.device = 0x40;
		ccb->ataio.cmd.sector_count = (uint8_t)sector_count;
		ccb->ataio.cmd.sector_count_exp = (uint8_t)(sector_count >> 8);
		ccb->ataio.cmd.control = 0;
	}

	ccb->ccb_h.flags |= CAM_DEV_QFRZDIS;

	rc = cam_send_ccb(fsmart->camdev, ccb);
	if (rc < 0) {
		warn("error sending read log ext command");
		rc = errno ? errno : EIO;
	} else if ((ccb->ccb_h.status & CAM_STATUS_MASK) != CAM_REQ_CMP) {
		if (do_debug)
			cam_error_print(fsmart->camdev, ccb, CAM_ESF_ALL,
					CAM_EPF_ALL, stderr);
		uint32_t cam_status = ccb->ccb_h.status & CAM_STATUS_MASK;
		switch (cam_status) {
		case CAM_CMD_TIMEOUT:
			rc = ETIMEDOUT;
			break;
		case CAM_REQ_ABORTED:
		case CAM_SCSI_BUS_RESET:
		case CAM_SEQUENCE_FAIL:
			rc = ECONNABORTED;
			break;
		default:
			rc = EIO;
			break;
		}
	}

	if (rc != 0)
		fsmart->last_cam_err = rc;

	cam_freeccb(ccb);
	return rc;
}

int32_t
device_write_smart_log(smart_h h, uint8_t log_addr, void *buf, size_t bsize)
{
	struct fbsd_smart *fsmart = h;
	union ccb *ccb = NULL;
	uint8_t smart_fis[12];
	int rc = 0;

	if (fsmart == NULL || buf == NULL || bsize != 512)
		return EINVAL;
	if (fsmart->read_only)
		return EROFS;

	fsmart->last_cam_err = 0;
	ccb = cam_getccb(fsmart->camdev);
	if (ccb == NULL)
		return ENOMEM;

	CCB_CLEAR_ALL_EXCEPT_HDR(ccb);

	if (fsmart->common.protocol != SMART_PROTO_ATA) {
		cam_freeccb(ccb);
		return ENODEV;
	}

	if (fsmart->common.info.tunneled) {
		struct ata_pass_16 *cdb;

		cdb = (struct ata_pass_16 *)ccb->csio.cdb_io.cdb_bytes;
		bzero(cdb, sizeof(*cdb));

		scsi_ata_pass_16(&ccb->csio,
				1, NULL,
				CAM_DIR_OUT,
				MSG_SIMPLE_Q_TAG,
				AP_PROTO_PIO_OUT,
				AP_FLAG_TDIR_TO_DEV |
				    AP_FLAG_BYT_BLOK_BLOCKS |
				    AP_FLAG_TLEN_SECT_CNT,
				0xD6,
				1,
				log_addr,
				ATA_SMART_CMD,
				0,
				buf, bsize,
				SSD_FULL_SIZE,
				30000);
		cdb->lba_mid = 0x4f;
		cdb->lba_high = 0xc2;
		cdb->device = 0;
	} else {
		memset(smart_fis, 0, sizeof(smart_fis));
		smart_fis[0] = ATA_SMART_CMD;
		smart_fis[1] = 0xD6;
		smart_fis[2] = log_addr;
		smart_fis[3] = 0x4f;
		smart_fis[4] = 0xc2;
		smart_fis[10] = 1;

		bcopy(smart_fis, &ccb->ataio.cmd.command, sizeof(smart_fis));

		cam_fill_ataio(&ccb->ataio,
				0, NULL,
				CAM_DIR_OUT,
				MSG_SIMPLE_Q_TAG,
				buf, bsize,
				30000);
		ccb->ataio.cmd.flags |= CAM_ATAIO_NEEDRESULT;
		ccb->ataio.cmd.control = 0;
	}

	ccb->ccb_h.flags |= CAM_DEV_QFRZDIS;

	rc = cam_send_ccb(fsmart->camdev, ccb);
	if (rc < 0) {
		warn("error sending write log command");
		rc = errno ? errno : EIO;
	} else if ((ccb->ccb_h.status & CAM_STATUS_MASK) != CAM_REQ_CMP) {
		if (do_debug)
			cam_error_print(fsmart->camdev, ccb, CAM_ESF_ALL,
					CAM_EPF_ALL, stderr);
		uint32_t cam_status = ccb->ccb_h.status & CAM_STATUS_MASK;
		switch (cam_status) {
		case CAM_CMD_TIMEOUT:
			rc = ETIMEDOUT;
			break;
		case CAM_REQ_ABORTED:
		case CAM_SCSI_BUS_RESET:
		case CAM_SEQUENCE_FAIL:
			rc = ECONNABORTED;
			break;
		default:
			rc = EIO;
			break;
		}
	}

	if (rc != 0)
		fsmart->last_cam_err = rc;

	cam_freeccb(ccb);
	return rc;
}

int32_t
device_smart_enable(smart_h h)
{
	struct fbsd_smart *fsmart = h;
	union ccb *ccb = NULL;
	uint8_t smart_fis[12];
	int rc = 0;

	if (fsmart == NULL)
		return EINVAL;
	if (fsmart->read_only)
		return EROFS;

	if (fsmart->common.protocol != SMART_PROTO_ATA)
		return ENODEV;

	fsmart->last_cam_err = 0;
	ccb = cam_getccb(fsmart->camdev);
	if (ccb == NULL)
		return ENOMEM;

	CCB_CLEAR_ALL_EXCEPT_HDR(ccb);

	if (fsmart->common.info.tunneled) {
		struct ata_pass_16 *cdb;

		cdb = (struct ata_pass_16 *)ccb->csio.cdb_io.cdb_bytes;
		bzero(cdb, sizeof(*cdb));

		scsi_ata_pass_16(&ccb->csio,
				1, NULL,
				CAM_DIR_NONE,
				MSG_SIMPLE_Q_TAG,
				AP_PROTO_NON_DATA,
					AP_FLAG_TDIR_FROM_DEV |
					    AP_FLAG_BYT_BLOK_BLOCKS,
				0xD8,
				0,
				0,
				ATA_SMART_CMD,
				0,
				NULL, 0,
				SSD_FULL_SIZE,
				30000);
		cdb->lba_mid = 0x4f;
		cdb->lba_high = 0xc2;
		cdb->device = 0;
	} else {
		memset(smart_fis, 0, sizeof(smart_fis));
		smart_fis[0] = ATA_SMART_CMD;
		smart_fis[1] = 0xD8;
		smart_fis[3] = 0x4f;
		smart_fis[4] = 0xc2;

		bcopy(smart_fis, &ccb->ataio.cmd.command, sizeof(smart_fis));

		cam_fill_ataio(&ccb->ataio,
				0, NULL,
				CAM_DIR_NONE,
				MSG_SIMPLE_Q_TAG,
				NULL, 0,
				30000);
		ccb->ataio.cmd.flags |= CAM_ATAIO_NEEDRESULT;
		ccb->ataio.cmd.control = 0;
	}

	ccb->ccb_h.flags |= CAM_DEV_QFRZDIS;

	rc = cam_send_ccb(fsmart->camdev, ccb);
	if (rc < 0) {
		warn("error sending smart enable command");
		rc = errno ? errno : EIO;
	} else if ((ccb->ccb_h.status & CAM_STATUS_MASK) != CAM_REQ_CMP) {
		uint32_t cam_status = ccb->ccb_h.status & CAM_STATUS_MASK;
		switch (cam_status) {
		case CAM_CMD_TIMEOUT:
			rc = ETIMEDOUT;
			break;
		case CAM_REQ_ABORTED:
		case CAM_SCSI_BUS_RESET:
		case CAM_SEQUENCE_FAIL:
			rc = ECONNABORTED;
			break;
		default:
			rc = EIO;
			break;
		}
	}

	if (rc != 0)
		fsmart->last_cam_err = rc;

	cam_freeccb(ccb);
	return rc;
}

int32_t
device_nvme_identify_ctrl(smart_h h, void *buf, size_t bsize)
{
	struct fbsd_smart *fsmart = h;
	union ccb *ccb = NULL;
	struct ccb_nvmeio *nvmeio;
	int rc = 0;

	if (fsmart == NULL)
		return EINVAL;

	if (fsmart->common.protocol != SMART_PROTO_NVME)
		return ENODEV;

	fsmart->last_cam_err = 0;
	ccb = cam_getccb(fsmart->camdev);
	if (ccb == NULL)
		return ENOMEM;

	CCB_CLEAR_ALL_EXCEPT_HDR(ccb);

	nvmeio = &ccb->nvmeio;
	nvmeio->cmd.opc = 0x06;
	nvmeio->cmd.nsid = NVME_GLOBAL_NAMESPACE_TAG;
	nvmeio->cmd.cdw10 = 1;

	cam_fill_nvmeadmin(&ccb->nvmeio,
			1, NULL,
			CAM_DIR_IN,
			buf, bsize,
			5000);

	ccb->ccb_h.flags |= CAM_DEV_QFRZDIS;

	rc = cam_send_ccb(fsmart->camdev, ccb);
	if (rc < 0) {
		warn("error sending nvme identify command");
		rc = errno ? errno : EIO;
	} else if ((ccb->ccb_h.status & CAM_STATUS_MASK) != CAM_REQ_CMP) {
		if (do_debug)
			cam_error_print(fsmart->camdev, ccb, CAM_ESF_ALL,
					CAM_EPF_ALL, stderr);
		uint32_t cam_status = ccb->ccb_h.status & CAM_STATUS_MASK;
		switch (cam_status) {
		case CAM_CMD_TIMEOUT:
			rc = ETIMEDOUT;
			break;
		case CAM_REQ_ABORTED:
		case CAM_SCSI_BUS_RESET:
		case CAM_SEQUENCE_FAIL:
			rc = ECONNABORTED;
			break;
		default:
			rc = EIO;
			break;
		}
	}

	if (rc != 0)
		fsmart->last_cam_err = rc;

	cam_freeccb(ccb);
	return rc;
}

int32_t
device_nvme_identify_ns(smart_h h, uint32_t nsid, void *buf, size_t bsize)
{
	struct fbsd_smart *fsmart = h;
	union ccb *ccb = NULL;
	struct ccb_nvmeio *nvmeio;
	int rc;

	if (fsmart == NULL || buf == NULL || bsize < 4096 || nsid == 0)
		return EINVAL;
	if (fsmart->common.protocol != SMART_PROTO_NVME)
		return ENODEV;

	fsmart->last_cam_err = 0;
	ccb = cam_getccb(fsmart->camdev);
	if (ccb == NULL)
		return ENOMEM;
	CCB_CLEAR_ALL_EXCEPT_HDR(ccb);
	nvmeio = &ccb->nvmeio;
	nvmeio->cmd.opc = 0x06;
	nvmeio->cmd.nsid = nsid;
	nvmeio->cmd.cdw10 = 0;
	cam_fill_nvmeadmin(&ccb->nvmeio, 1, NULL, CAM_DIR_IN, buf, bsize,
	    5000);
	ccb->ccb_h.flags |= CAM_DEV_QFRZDIS;
	rc = cam_send_ccb(fsmart->camdev, ccb);
	if (rc < 0)
		rc = errno ? errno : EIO;
	else if ((ccb->ccb_h.status & CAM_STATUS_MASK) != CAM_REQ_CMP) {
		if (do_debug)
			cam_error_print(fsmart->camdev, ccb, CAM_ESF_ALL,
			    CAM_EPF_ALL, stderr);
		switch (ccb->ccb_h.status & CAM_STATUS_MASK) {
		case CAM_CMD_TIMEOUT:
			rc = ETIMEDOUT;
			break;
		case CAM_REQ_ABORTED:
		case CAM_SCSI_BUS_RESET:
		case CAM_SEQUENCE_FAIL:
			rc = ECONNABORTED;
			break;
		default:
			rc = EIO;
			break;
		}
	} else
		rc = 0;
	if (rc != 0)
		fsmart->last_cam_err = rc;
	cam_freeccb(ccb);
	return rc;
}

int32_t
device_read_log(smart_h h, uint32_t page, void *buf, size_t bsize)
{
	struct fbsd_smart *fsmart = h;
	union ccb *ccb = NULL;
	int rc = 0;

	if (fsmart == NULL)
		return EINVAL;

	fsmart->last_cam_err = 0;
	dprintf("read log page %#x\n", page);

	switch (fsmart->common.protocol) {
	case SMART_PROTO_SCSI:
		return __device_scsi_log_sense(fsmart, page, buf, bsize);
	case SMART_PROTO_ATA:
	case SMART_PROTO_NVME:
		break;
	default:
		warnx("unsupported protocol %d", fsmart->common.protocol);
		return ENODEV;
	}

	ccb = cam_getccb(fsmart->camdev);
	if (ccb == NULL)
		return ENOMEM;

	CCB_CLEAR_ALL_EXCEPT_HDR(ccb);

	switch (fsmart->common.protocol) {
	case SMART_PROTO_ATA:
		rc = __device_read_ata(h, page, buf, bsize, ccb);
		break;
	case SMART_PROTO_NVME:
		rc = __device_read_nvme(h, page, buf, bsize, ccb);
		break;
	default:
		rc = ENODEV;
		break;
	}

	if (rc) {
		if (rc == EINVAL)
			warnx("unsupported page %#x", page);

		cam_freeccb(ccb);
		return rc;
	}

	ccb->ccb_h.flags |= CAM_DEV_QFRZDIS;

	rc = cam_send_ccb(fsmart->camdev, ccb);
	if ((rc < 0) || ((ccb->ccb_h.status & CAM_STATUS_MASK) != CAM_REQ_CMP)) {
		if (rc < 0) {
			warn("error sending command");
			rc = errno ? errno : EIO;
		}
	}

	/*
	 * Most commands don't need any post-processing. But then there's
	 * ATA. It's why we can't have nice things :(
	 */
	switch (fsmart->common.protocol) {
	case SMART_PROTO_ATA:
		__device_status_ata(h, ccb);
		break;
	default:
		;
	}

	if ((ccb->ccb_h.status & CAM_STATUS_MASK) != CAM_REQ_CMP) {
		if (do_debug)
			cam_error_print(fsmart->camdev, ccb, CAM_ESF_ALL,
					CAM_EPF_ALL, stderr);
		if (rc >= 0) {
			uint32_t cam_status = ccb->ccb_h.status & CAM_STATUS_MASK;
			switch (cam_status) {
			case CAM_CMD_TIMEOUT:
				rc = ETIMEDOUT;
				break;
			case CAM_REQ_ABORTED:
			case CAM_SCSI_BUS_RESET:
			case CAM_SEQUENCE_FAIL:
				rc = ECONNABORTED;
				break;
			default:
				rc = EIO;
				break;
			}
		}
	}

	if (rc != 0)
		fsmart->last_cam_err = rc;

	cam_freeccb(ccb);

	return rc;
}

/*
 * The SCSI / ATA Translation (SAT) requires devices to support the ATA
 * Information VPD Page (T10/2126-D Revision 04). Use the existence of
 * this page to identify tunneled devices.
 */
static bool
__device_proto_tunneled(struct fbsd_smart *fsmart)
{
	union ccb *ccb = NULL;
	struct scsi_vpd_supported_page_list supportedp;
	uint32_t i;
	bool is_tunneled = false;

	if (fsmart->common.protocol != SMART_PROTO_SCSI) {
		return false;
	}

	ccb = cam_getccb(fsmart->camdev);
	if (!ccb) {
		warn("Allocation failure ccb=%p", ccb);
		goto __device_proto_tunneled_out;
	}

	scsi_inquiry(&ccb->csio,
			3, // retries
			NULL, // callback function
			MSG_SIMPLE_Q_TAG, // tag action
			(uint8_t *)&supportedp,
			sizeof(struct scsi_vpd_supported_page_list),
			1, // EVPD
			SVPD_SUPPORTED_PAGE_LIST, // page code
			SSD_FULL_SIZE, // sense length
			30000); // timeout

	ccb->ccb_h.flags |= CAM_DEV_QFRZDIS;

	if ((cam_send_ccb(fsmart->camdev, ccb) >= 0) &&
			((ccb->ccb_h.status & CAM_STATUS_MASK) == CAM_REQ_CMP)) {
		dprintf("Looking for page %#x (total = %u):\n", SVPD_ATA_INFORMATION,
				supportedp.length);
		for (i = 0; i < supportedp.length; i++) {
			dprintf("\t[%u] = %#x\n", i, supportedp.list[i]);
			if (supportedp.list[i] == SVPD_ATA_INFORMATION) {
				is_tunneled = true;
				break;
			}
		}
	}

	cam_freeccb(ccb);

__device_proto_tunneled_out:
	return is_tunneled;
}

/**
 * Retrieve the device protocol type via the transport settings
 *
 * @return protocol type or SMART_PROTO_MAX on error
 */
static smart_protocol_e
__device_get_proto(struct fbsd_smart *fsmart)
{
	smart_protocol_e proto = SMART_PROTO_MAX;
	union ccb *ccb;

	if (!fsmart || !fsmart->camdev) {
		warn("Bad handle %p", fsmart);
		return proto;
	}

	ccb = cam_getccb(fsmart->camdev);
	if (ccb != NULL) {
		CCB_CLEAR_ALL_EXCEPT_HDR(&ccb->cts);

		ccb->ccb_h.func_code = XPT_GET_TRAN_SETTINGS;
		ccb->cts.type = CTS_TYPE_CURRENT_SETTINGS;

		if (cam_send_ccb(fsmart->camdev, ccb) >= 0) {
			if ((ccb->ccb_h.status & CAM_STATUS_MASK) == CAM_REQ_CMP) {
				struct ccb_trans_settings *cts = &ccb->cts;

				switch (cts->protocol) {
				case PROTO_ATA:
					proto = SMART_PROTO_ATA;
					break;
				case PROTO_SCSI:
					proto = SMART_PROTO_SCSI;
					break;
				case PROTO_NVME:
					proto = SMART_PROTO_NVME;
					break;
				default:
				dprintf("%s: unknown protocol %d\n",
						__func__,
						cts->protocol);
				}
			}
		}

		cam_freeccb(ccb);
	}

	return proto;
}

static int32_t
__device_ata_sector_count(struct ata_params *ident, uint64_t *sectors)
{
	if (ident == NULL || sectors == NULL)
		return EINVAL;
	if (ident->support.command2 & ATA_SUPPORT_ADDRESS48) {
		*sectors = (uint64_t)ident->lba_size48_1 |
		    (uint64_t)ident->lba_size48_2 << 16 |
		    (uint64_t)ident->lba_size48_3 << 32 |
		    (uint64_t)ident->lba_size48_4 << 48;
	} else {
		*sectors = (uint64_t)ident->lba_size_1 |
		    (uint64_t)ident->lba_size_2 << 16;
	}
	return *sectors == 0 ? EINVAL : 0;
}

static int32_t
__device_info_ata(struct fbsd_smart *fsmart, struct ccb_getdev *cgd)
{
	smart_info_t *sinfo = NULL;

	if (!fsmart || !cgd) {
		return -1;
	}

	sinfo = &fsmart->common.info;
	
	sinfo->supported = cgd->ident_data.support.command1 &
		ATA_SUPPORT_SMART;
	sinfo->sct_supported = (cgd->ident_data.sct & 0x0001) != 0;
	sinfo->self_test_supported =
		(cgd->ident_data.support.extension & 0xc000) == 0x4000 &&
		(cgd->ident_data.support.extension & ATA_SUPPORT_SMARTTEST) != 0;
	(void)__device_ata_sector_count(&cgd->ident_data, &sinfo->sector_count);

	dprintf("ATA command1 = %#x\n", cgd->ident_data.support.command1);

	cam_strvis((uint8_t *)sinfo->device, cgd->ident_data.model,
			sizeof(cgd->ident_data.model),
			sizeof(sinfo->device));
	cam_strvis((uint8_t *)sinfo->rev, cgd->ident_data.revision,
			sizeof(cgd->ident_data.revision),
			sizeof(sinfo->rev));
	cam_strvis((uint8_t *)sinfo->serial, cgd->ident_data.serial,
			sizeof(cgd->ident_data.serial),
			sizeof(sinfo->serial));

	return 0;
}

static int32_t
__device_info_scsi(struct fbsd_smart *fsmart, struct ccb_getdev *cgd)
{
	smart_info_t *sinfo = NULL;
	union ccb *ccb = NULL;
	struct scsi_vpd_unit_serial_number *snum = NULL;
	struct scsi_log_informational_exceptions ie = {0};

	if (!fsmart || !cgd) {
		return -1;
	}

	sinfo = &fsmart->common.info;

	cam_strvis((uint8_t *)sinfo->vendor, (uint8_t *)cgd->inq_data.vendor,
			sizeof(cgd->inq_data.vendor),
			sizeof(sinfo->vendor));
	cam_strvis((uint8_t *)sinfo->device, (uint8_t *)cgd->inq_data.product,
			sizeof(cgd->inq_data.product),
			sizeof(sinfo->device));
	cam_strvis((uint8_t *)sinfo->rev, (uint8_t *)cgd->inq_data.revision,
			sizeof(cgd->inq_data.revision),
			sizeof(sinfo->rev));

	sinfo->scsi_version = cgd->inq_data.version;

	ccb = cam_getccb(fsmart->camdev);
	snum = malloc(sizeof(struct scsi_vpd_unit_serial_number));
	if (!ccb || !snum) {
		warn("Allocation failure ccb=%p snum=%p", ccb, snum);
		goto __device_info_scsi_out;
	}

	/* Get the serial number */
	CCB_CLEAR_ALL_EXCEPT_HDR(&ccb->csio);

	scsi_inquiry(&ccb->csio,
			3, // retries
			NULL, // callback function
			MSG_SIMPLE_Q_TAG, // tag action
			(uint8_t *)snum,
			sizeof(struct scsi_vpd_unit_serial_number),
			1, // EVPD
			SVPD_UNIT_SERIAL_NUMBER, // page code
			SSD_FULL_SIZE, // sense length
			30000); // timeout

	ccb->ccb_h.flags |= CAM_DEV_QFRZDIS;

	if ((cam_send_ccb(fsmart->camdev, ccb) >= 0) &&
			((ccb->ccb_h.status & CAM_STATUS_MASK) == CAM_REQ_CMP)) {
		size_t serial_len = snum->length;
		if (serial_len > sizeof(snum->serial_num))
			serial_len = sizeof(snum->serial_num);
		cam_strvis((uint8_t *)sinfo->serial, snum->serial_num,
				serial_len,
				sizeof(sinfo->serial));
		sinfo->serial[sizeof(sinfo->serial) - 1] = '\0';
	}

	CCB_CLEAR_ALL_EXCEPT_HDR(ccb);

	scsi_log_sense(&ccb->csio,
			/* retries */1,
			/* cbfcnp */NULL,
			/* tag_action */0,
			/* page_code */SLS_PAGE_CTRL_CUMULATIVE,
			/* page */SLS_IE_PAGE,
			/* save_pages */0,
			/* ppc */0,
			/* paramptr */0,
			/* param_buf */(uint8_t *)&ie,
			/* param_len */sizeof(ie),
			/* sense_len */0,
			/* timeout */30000);

	/*
	 * Note: The existance of the Informational Exceptions (IE) log page
	 *       appears to be the litmus test for SMART support in SCSI
	 *       devices. Confusingly, smartctl will report SMART health
	 *       status as 'OK' if the device doesn't support the IE page.
	 *       For now, just report the facts.
	 */
	if ((cam_send_ccb(fsmart->camdev, ccb) >= 0) &&
			((ccb->ccb_h.status & CAM_STATUS_MASK) == CAM_REQ_CMP)) {
		if (ie.hdr.param_len < 4) {
			dprintf("Log Sense, Informational Exceptions failed "
					"(length=%u asc=%#x ascq=%#x)\n",
					ie.hdr.param_len, ie.ie_asc, ie.ie_ascq); 
		} else {
			sinfo->supported = true;
		}
	}

__device_info_scsi_out:
	free(snum);
	if (ccb)
		cam_freeccb(ccb);

	return 0;
}

static int32_t
__device_info_nvme(struct fbsd_smart *fsmart, struct ccb_getdev *cgd)
{
	union ccb *ccb;
	smart_info_t *sinfo = NULL;
	struct nvme_controller_data cd;
	int32_t rc = -1;

	if (!fsmart || !cgd) {
		return -1;
	}

	sinfo = &fsmart->common.info;
	
	sinfo->supported = true;

	ccb = cam_getccb(fsmart->camdev);
	if (ccb != NULL) {
		struct ccb_dev_advinfo *cdai = &ccb->cdai;

		CCB_CLEAR_ALL_EXCEPT_HDR(cdai);

		cdai->ccb_h.func_code = XPT_DEV_ADVINFO;
		cdai->ccb_h.flags = CAM_DIR_IN;
		cdai->flags = CDAI_FLAG_NONE;
#ifdef CDAI_TYPE_NVME_CNTRL
		cdai->buftype = CDAI_TYPE_NVME_CNTRL;
#else
		cdai->buftype = 6;
#endif
		cdai->bufsiz = sizeof(struct nvme_controller_data);
		cdai->buf = (uint8_t *)&cd;

			if (cam_send_ccb(fsmart->camdev, ccb) >= 0) {
			if ((ccb->ccb_h.status & CAM_STATUS_MASK) == CAM_REQ_CMP) {
				sinfo->nvme_version = cd.ver;
				cam_strvis((uint8_t *)sinfo->device, cd.mn,
						sizeof(cd.mn),
						sizeof(sinfo->device));
				cam_strvis((uint8_t *)sinfo->rev, cd.fr,
						sizeof(cd.fr),
						sizeof(sinfo->rev));
				cam_strvis((uint8_t *)sinfo->serial, cd.sn,
						sizeof(cd.sn),
						sizeof(sinfo->serial));
				rc = 0;
			}
			}

			CCB_CLEAR_ALL_EXCEPT_HDR(&ccb->cpi);
			ccb->cpi.ccb_h.func_code = XPT_PATH_INQ;
			if (cam_send_ccb(fsmart->camdev, ccb) >= 0 &&
			    (ccb->ccb_h.status & CAM_STATUS_MASK) == CAM_REQ_CMP &&
			    ccb->cpi.protocol == PROTO_NVME)
				sinfo->nvme_nsid = ccb->cpi.xport_specific.nvme.nsid;

			cam_freeccb(ccb);
	}

	return rc;
}

static int32_t
__device_info_tunneled_ata(struct fbsd_smart *fsmart)
{
	struct ata_params ident_data;
	union ccb *ccb = NULL;
	int32_t rc = -1;

	ccb = cam_getccb(fsmart->camdev);
	if (ccb == NULL) {
		goto __device_info_tunneled_ata_out;
	}

	bzero(&ident_data, sizeof(struct ata_params));

	CCB_CLEAR_ALL_EXCEPT_HDR(ccb);

	scsi_ata_pass_16(&ccb->csio,
			/*retries*/	1,
			/*cbfcnp*/	NULL,
			/*flags*/	CAM_DIR_IN,
			/*tag_action*/	MSG_SIMPLE_Q_TAG,
			/*protocol*/	AP_PROTO_PIO_IN,
			/*ata_flags*/	AP_FLAG_TLEN_SECT_CNT |
					AP_FLAG_BYT_BLOK_BLOCKS |
					AP_FLAG_TDIR_FROM_DEV,
			/*features*/	0,
			1,
			/*lba*/		0,
			/*command*/	ATA_ATA_IDENTIFY,
			/*control*/	0,
			/*data_ptr*/	(uint8_t *)&ident_data,
			/*dxfer_len*/	sizeof(struct ata_params),
			/*sense_len*/	SSD_FULL_SIZE,
			30000
			);

	rc = cam_send_ccb(fsmart->camdev, ccb);
	if (rc != 0) {
		warnx("%s: scsi_ata_pass_16() failed (programmer error?)",
				__func__);
		goto __device_info_tunneled_ata_out;
	}

	if ((ccb->ccb_h.status & CAM_STATUS_MASK) != CAM_REQ_CMP) {
		rc = -1;
		goto __device_info_tunneled_ata_out;
	}

	fsmart->common.info.supported = ident_data.support.command1 &
		ATA_SUPPORT_SMART;
	fsmart->common.info.sct_supported = (ident_data.sct & 0x0001) != 0;
	fsmart->common.info.self_test_supported =
		(ident_data.support.extension & 0xc000) == 0x4000 &&
		(ident_data.support.extension & ATA_SUPPORT_SMARTTEST) != 0;
	(void)__device_ata_sector_count(&ident_data,
	    &fsmart->common.info.sector_count);
	cam_strvis((uint8_t *)fsmart->common.info.device, ident_data.model,
			sizeof(ident_data.model), sizeof(fsmart->common.info.device));
	cam_strvis((uint8_t *)fsmart->common.info.rev, ident_data.revision,
			sizeof(ident_data.revision), sizeof(fsmart->common.info.rev));
	cam_strvis((uint8_t *)fsmart->common.info.serial, ident_data.serial,
			sizeof(ident_data.serial), sizeof(fsmart->common.info.serial));

	dprintf("ATA command1 = %#x\n", ident_data.support.command1);

__device_info_tunneled_ata_out:
	if (ccb) {
		cam_freeccb(ccb);
	}

	return rc;
}

/**
 * Retrieve the device information and use to populate the info structure
 */
static int32_t
__device_get_info(struct fbsd_smart *fsmart)
{
	union ccb *ccb;
	int32_t rc = -1;

	if (!fsmart || !fsmart->camdev) {
		warn("Bad handle %p", fsmart);
		return -1;
	}

	ccb = cam_getccb(fsmart->camdev);
	if (ccb != NULL) {
		struct ccb_getdev *cgd = &ccb->cgd;

		CCB_CLEAR_ALL_EXCEPT_HDR(cgd);

		/*
		 * GDEV_TYPE doesn't support NVMe. What we do get is:
		 *  - device (ata/model, scsi/product)
		 *  - revision (ata, scsi)
		 *  - serial (ata)
		 *  - vendor (scsi)
		 *  - supported (ata)
		 *
		 *  Serial # for all proto via ccb_dev_advinfo (buftype CDAI_TYPE_SERIAL_NUM)
		 */
		ccb->ccb_h.func_code = XPT_GDEV_TYPE;

		if (cam_send_ccb(fsmart->camdev, ccb) >= 0) {
			if ((ccb->ccb_h.status & CAM_STATUS_MASK) == CAM_REQ_CMP) {
				switch (cgd->protocol) {
				case PROTO_ATA:
					rc = __device_info_ata(fsmart, cgd);
					break;
				case PROTO_SCSI:
					rc = __device_info_scsi(fsmart, cgd);
					if (!rc && fsmart->common.protocol == SMART_PROTO_ATA) {
						rc = __device_info_tunneled_ata(fsmart);
					}
					break;
				case PROTO_NVME:
					rc = __device_info_nvme(fsmart, cgd);
					break;
				default:
				dprintf("%s: unsupported protocol %d\n",
						__func__, cgd->protocol);
				}
			}
		}

		cam_freeccb(ccb);
	}

	return rc;
}
