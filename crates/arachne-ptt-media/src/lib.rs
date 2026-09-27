use sha2::{Digest, Sha256};
#[cfg(unix)]
use std::os::unix::fs::{OpenOptionsExt, PermissionsExt};
use std::{
    collections::BTreeMap,
    fs::{self, File, OpenOptions},
    io::{self, Read, Seek, SeekFrom, Write},
    path::{Path, PathBuf},
};

pub const SAMPLE_RATE: u32 = 48_000;
pub const FRAME_SAMPLES: usize = 960;
pub const FRAMES_PER_BATCH: usize = 5;
pub const SAMPLES_PER_BATCH: u64 = (FRAME_SAMPLES * FRAMES_PER_BATCH) as u64;
pub const MAX_BATCH_COUNT: usize = 300;
pub const BITRATE: u32 = 24_000;
pub const MAX_OPUS_PACKET: usize = 1_275;
pub const MAX_BATCH_BYTES: usize = 4_096;
pub const MAX_RECORDING_BYTES: usize = 512 * 1024;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Start {
    pub transmission: [u8; 16],
    pub batch_count: u16,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Batch {
    pub transmission: [u8; 16],
    pub index: u16,
    pub sample_offset: u64,
    pub packets: Vec<Vec<u8>>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct End {
    pub transmission: [u8; 16],
    pub batch_count: u16,
    pub digest: [u8; 32],
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Message {
    Start(Start),
    Batch(Batch),
    End(End),
}

struct Reader<'a> {
    bytes: &'a [u8],
    offset: usize,
}

impl<'a> Reader<'a> {
    fn new(bytes: &'a [u8]) -> io::Result<Self> {
        if bytes.len() > MAX_BATCH_BYTES {
            return Err(invalid("application envelope exceeds 4096 bytes"));
        }
        Ok(Self { bytes, offset: 0 })
    }

    fn take(&mut self, length: usize) -> io::Result<&'a [u8]> {
        let end = self
            .offset
            .checked_add(length)
            .ok_or_else(|| invalid("envelope length overflow"))?;
        let value = self
            .bytes
            .get(self.offset..end)
            .ok_or_else(|| invalid("truncated application envelope"))?;
        self.offset = end;
        Ok(value)
    }

    fn array<const N: usize>(&mut self) -> io::Result<[u8; N]> {
        self.take(N)?
            .try_into()
            .map_err(|_| invalid("invalid fixed-width field"))
    }

    fn u8(&mut self) -> io::Result<u8> {
        Ok(self.array::<1>()?[0])
    }
    fn u16(&mut self) -> io::Result<u16> {
        Ok(u16::from_be_bytes(self.array()?))
    }
    fn u32(&mut self) -> io::Result<u32> {
        Ok(u32::from_be_bytes(self.array()?))
    }
    fn u64(&mut self) -> io::Result<u64> {
        Ok(u64::from_be_bytes(self.array()?))
    }

    fn finish(self) -> io::Result<()> {
        if self.offset == self.bytes.len() {
            Ok(())
        } else {
            Err(invalid("trailing application envelope bytes"))
        }
    }
}

fn invalid(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, message)
}

fn header(kind: u8, transmission: [u8; 16]) -> Vec<u8> {
    let mut bytes = Vec::with_capacity(MAX_BATCH_BYTES);
    bytes.extend_from_slice(b"APTT");
    bytes.extend_from_slice(&[1, kind]);
    bytes.extend_from_slice(&transmission);
    bytes
}

pub fn encode_start(transmission: [u8; 16], batch_count: u16) -> io::Result<Vec<u8>> {
    if !(1..=MAX_BATCH_COUNT as u16).contains(&batch_count) {
        return Err(invalid("invalid transmission batch limit"));
    }
    let mut bytes = header(1, transmission);
    bytes.extend_from_slice(&SAMPLE_RATE.to_be_bytes());
    bytes.push(1);
    bytes.extend_from_slice(&BITRATE.to_be_bytes());
    bytes.extend_from_slice(&(FRAME_SAMPLES as u16).to_be_bytes());
    bytes.push(FRAMES_PER_BATCH as u8);
    bytes.extend_from_slice(&batch_count.to_be_bytes());
    Ok(bytes)
}

pub fn encode_batch(
    transmission: [u8; 16],
    index: u16,
    packets: &[Vec<u8>],
) -> io::Result<Vec<u8>> {
    if usize::from(index) >= MAX_BATCH_COUNT
        || packets.is_empty()
        || packets.len() > FRAMES_PER_BATCH
    {
        return Err(invalid("invalid batch index or packet count"));
    }
    let mut bytes = header(2, transmission);
    bytes.extend_from_slice(&index.to_be_bytes());
    bytes.extend_from_slice(&(u64::from(index) * SAMPLES_PER_BATCH).to_be_bytes());
    bytes.push(packets.len() as u8);
    for packet in packets {
        if !(1..=MAX_OPUS_PACKET).contains(&packet.len()) {
            return Err(invalid("invalid Opus packet length"));
        }
        bytes.extend_from_slice(&(packet.len() as u16).to_be_bytes());
        bytes.extend_from_slice(packet);
    }
    if bytes.len() > MAX_BATCH_BYTES {
        return Err(invalid("application envelope exceeds 4096 bytes"));
    }
    Ok(bytes)
}

pub fn encode_end(
    transmission: [u8; 16],
    batch_count: u16,
    digest: [u8; 32],
) -> io::Result<Vec<u8>> {
    if batch_count > MAX_BATCH_COUNT as u16 {
        return Err(invalid("invalid End batch count"));
    }
    let mut bytes = header(3, transmission);
    bytes.extend_from_slice(&batch_count.to_be_bytes());
    bytes.extend_from_slice(&digest);
    Ok(bytes)
}

pub fn parse_message(bytes: &[u8]) -> io::Result<Message> {
    let mut reader = Reader::new(bytes)?;
    if reader.array::<4>()? != *b"APTT" || reader.u8()? != 1 {
        return Err(invalid("unknown application envelope or version"));
    }
    let kind = reader.u8()?;
    let transmission = reader.array::<16>()?;
    let message = match kind {
        1 => {
            if reader.u32()? != SAMPLE_RATE
                || reader.u8()? != 1
                || reader.u32()? != BITRATE
                || reader.u16()? as usize != FRAME_SAMPLES
                || reader.u8()? as usize != FRAMES_PER_BATCH
            {
                return Err(invalid("unsupported Opus codec description"));
            }
            let batch_count = reader.u16()?;
            if !(1..=MAX_BATCH_COUNT as u16).contains(&batch_count) {
                return Err(invalid("invalid transmission batch count"));
            }
            Message::Start(Start {
                transmission,
                batch_count,
            })
        }
        2 => {
            let index = reader.u16()?;
            let sample_offset = reader.u64()?;
            let packet_count = reader.u8()? as usize;
            if usize::from(index) >= MAX_BATCH_COUNT
                || sample_offset != u64::from(index) * SAMPLES_PER_BATCH
                || !(1..=FRAMES_PER_BATCH).contains(&packet_count)
            {
                return Err(invalid("invalid batch index, offset, or packet count"));
            }
            let mut packets = Vec::with_capacity(packet_count);
            for _ in 0..packet_count {
                let length = usize::from(reader.u16()?);
                if !(1..=MAX_OPUS_PACKET).contains(&length) {
                    return Err(invalid("invalid Opus packet length"));
                }
                packets.push(reader.take(length)?.to_vec());
            }
            Message::Batch(Batch {
                transmission,
                index,
                sample_offset,
                packets,
            })
        }
        3 => {
            let batch_count = reader.u16()?;
            if batch_count > MAX_BATCH_COUNT as u16 {
                return Err(invalid("invalid End batch count"));
            }
            Message::End(End {
                transmission,
                batch_count,
                digest: reader.array()?,
            })
        }
        _ => return Err(invalid("unknown application envelope kind")),
    };
    reader.finish()?;
    Ok(message)
}

pub fn record_id(transmission: [u8; 16], kind: u8, index: u16) -> [u8; 16] {
    let mut digest = Sha256::new();
    digest.update(b"arachne-ptt-record-v1");
    digest.update(transmission);
    digest.update([kind]);
    digest.update(index.to_be_bytes());
    digest.finalize()[..16]
        .try_into()
        .expect("fixed digest length")
}

pub fn packet_digest<'a>(batches: impl IntoIterator<Item = &'a [Vec<u8>]>) -> [u8; 32] {
    let mut digest = Sha256::new();
    for packets in batches {
        for packet in packets {
            digest.update((packet.len() as u16).to_be_bytes());
            digest.update(packet);
        }
    }
    digest.finalize().into()
}

pub fn recording_identity(
    payload: &[u8],
    transmission: [u8; 16],
    id: [u8; 16],
) -> io::Result<(u8, u16)> {
    match parse_message(payload)? {
        Message::Start(start)
            if start.transmission == transmission && id == record_id(transmission, 1, 0) =>
        {
            Ok((1, 0))
        }
        Message::Batch(batch)
            if batch.transmission == transmission
                && id == record_id(transmission, 2, batch.index) =>
        {
            Ok((2, batch.index))
        }
        Message::End(end)
            if end.transmission == transmission && id == record_id(transmission, 3, 0) =>
        {
            Ok((3, 0))
        }
        _ => Err(invalid("recording ID, message, and transmission disagree")),
    }
}

#[derive(Clone)]
pub struct StoredRecord {
    pub id: [u8; 16],
    pub sequence: u64,
    pub kind: u8,
    pub index: u16,
    pub offset: u64,
    pub payload: Vec<u8>,
}

pub struct RecordingLog {
    path: PathBuf,
    transmission: [u8; 16],
    bytes: usize,
    records: BTreeMap<[u8; 16], StoredRecord>,
}

impl RecordingLog {
    pub fn open(path: &Path, transmission: [u8; 16]) -> io::Result<Self> {
        let mut file = private_file(path, false)?;
        #[cfg(unix)]
        file.set_permissions(fs::Permissions::from_mode(0o600))?;
        if file.metadata()?.len() > MAX_RECORDING_BYTES as u64 {
            return Err(invalid("private recording exceeds its byte budget"));
        }
        let mut bytes = Vec::new();
        file.read_to_end(&mut bytes)?;
        if bytes.is_empty() {
            file.write_all(b"APLG")?;
            file.sync_data()?;
            bytes.extend_from_slice(b"APLG");
        }
        if bytes.len() < 4 || &bytes[..4] != b"APLG" {
            return Err(invalid("invalid private recording header"));
        }
        let mut records: BTreeMap<[u8; 16], StoredRecord> = BTreeMap::new();
        let mut offset = 4usize;
        while offset < bytes.len() {
            let start = offset;
            if bytes.len() - offset < 4 {
                break;
            }
            let length = u32::from_be_bytes(bytes[offset..offset + 4].try_into().unwrap()) as usize;
            offset += 4;
            if length < 25 || length > MAX_BATCH_BYTES + 24 {
                return Err(invalid("invalid private recording record length"));
            }
            let Some(end) = offset.checked_add(length) else {
                return Err(invalid("private recording length overflow"));
            };
            if end > bytes.len() {
                offset = start;
                break;
            }
            let id: [u8; 16] = bytes[offset..offset + 16].try_into().unwrap();
            let sequence = u64::from_be_bytes(bytes[offset + 16..offset + 24].try_into().unwrap());
            let payload = bytes[offset + 24..end].to_vec();
            let (kind, index) = recording_identity(&payload, transmission, id)?;
            let record = StoredRecord {
                id,
                sequence,
                kind,
                index,
                offset: start as u64,
                payload,
            };
            if let Some(previous) = records.get(&id) {
                if previous.sequence != record.sequence || previous.payload != record.payload {
                    return Err(invalid("conflicting private recording record"));
                }
            } else {
                records.insert(id, record);
            }
            offset = end;
        }
        if offset != bytes.len() {
            file.set_len(offset as u64)?;
            file.sync_data()?;
            bytes.truncate(offset);
        }
        if bytes.len() > MAX_RECORDING_BYTES {
            return Err(invalid("private recording exceeds its byte budget"));
        }
        Ok(Self {
            path: path.to_owned(),
            transmission,
            bytes: bytes.len(),
            records,
        })
    }

    pub fn append(&mut self, id: [u8; 16], sequence: u64, payload: &[u8]) -> io::Result<bool> {
        if payload.len() > MAX_BATCH_BYTES {
            return Err(invalid("recording object exceeds its bounds"));
        }
        let (kind, index) = recording_identity(payload, self.transmission, id)?;
        if let Some(previous) = self.records.get(&id) {
            // The PTT record ID binds immutable content; a redelivery can carry a new workspace counter.
            if previous.payload != payload {
                return Err(invalid("conflicting duplicate recording ID"));
            }
            return Ok(false);
        }
        let record_length = 24usize + payload.len();
        let total = self.bytes.saturating_add(4 + record_length);
        if total > MAX_RECORDING_BYTES {
            return Err(invalid("private recording exceeds its byte budget"));
        }
        let mut file = private_file(&self.path, false)?;
        file.seek(SeekFrom::End(0))?;
        file.write_all(&(record_length as u32).to_be_bytes())?;
        file.write_all(&id)?;
        file.write_all(&sequence.to_be_bytes())?;
        file.write_all(payload)?;
        self.records.insert(
            id,
            StoredRecord {
                id,
                sequence,
                kind,
                index,
                offset: self.bytes as u64,
                payload: payload.to_vec(),
            },
        );
        self.bytes = total;
        Ok(true)
    }

    pub fn commit(&mut self) -> io::Result<()> {
        private_file(&self.path, false)?.sync_data()
    }

    /// Last saved publisher sequence in the recording's contiguous Start/batch prefix.
    /// None means no reliable cursor; this does not claim earlier workspace history.
    pub fn recovery_cursor(&self) -> Option<u64> {
        let mut after = self
            .records
            .get(&record_id(self.transmission, 1, 0))?
            .sequence;
        if after == 0 {
            return None;
        }
        for index in 0..MAX_BATCH_COUNT as u16 {
            let Some(record) = self.records.get(&record_id(self.transmission, 2, index)) else {
                break;
            };
            // Re-published metadata may have a newer sequence than the original media.
            // It is not a safe cursor across those earlier objects.
            if record.sequence <= after {
                return None;
            }
            after = record.sequence;
        }
        Some(after)
    }

    pub fn records(&self) -> impl Iterator<Item = &StoredRecord> {
        self.records.values()
    }
    pub fn bytes(&self) -> usize {
        self.bytes
    }
}

fn private_file(path: &Path, truncate: bool) -> io::Result<File> {
    let mut options = OpenOptions::new();
    options
        .read(true)
        .write(true)
        .create(true)
        .truncate(truncate);
    #[cfg(unix)]
    options.mode(0o600);
    let file = options.open(path)?;
    #[cfg(unix)]
    if truncate {
        file.set_permissions(fs::Permissions::from_mode(0o600))?;
    }
    Ok(file)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn recovery_cursor_stops_at_the_first_missing_batch_and_survives_reopen() {
        let path = std::env::temp_dir().join(format!(
            "ptt-cursor-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        let id = [0x72; 16];
        let mut log = RecordingLog::open(&path, id).unwrap();
        assert_eq!(log.recovery_cursor(), None);
        log.append(record_id(id, 1, 0), 100, &encode_start(id, 3).unwrap())
            .unwrap();
        assert_eq!(log.recovery_cursor(), Some(100));
        for (index, sequence, expected) in [(0, 102, 102), (2, 105, 102), (1, 104, 105)] {
            log.append(
                record_id(id, 2, index),
                sequence,
                &encode_batch(id, index, &[vec![1]]).unwrap(),
            )
            .unwrap();
            assert_eq!(log.recovery_cursor(), Some(expected));
        }
        log.commit().unwrap();
        drop(log);
        let log = RecordingLog::open(&path, id).unwrap();
        assert_eq!(log.recovery_cursor(), Some(105));
        fs::remove_file(&path).unwrap();

        let mut log = RecordingLog::open(&path, id).unwrap();
        log.append(
            record_id(id, 2, 0),
            10,
            &encode_batch(id, 0, &[vec![1]]).unwrap(),
        )
        .unwrap();
        assert_eq!(
            log.recovery_cursor(),
            None,
            "missing Start has no trusted prefix"
        );
        log.append(record_id(id, 1, 0), 20, &encode_start(id, 3).unwrap())
            .unwrap();
        assert_eq!(
            log.recovery_cursor(),
            None,
            "republished Start cannot skip earlier media"
        );
        fs::remove_file(path).unwrap();
    }

    #[test]
    fn recording_redelivery_keeps_one_immutable_receipt() {
        let transmission = [0x71; 16];
        let id = record_id(transmission, 1, 0);
        let payload = encode_start(transmission, 1).unwrap();
        let path = std::env::temp_dir().join(format!(
            "arachne-recording-redelivery-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        let mut log = RecordingLog::open(&path, transmission).unwrap();
        assert!(log.append(id, 11, &payload).unwrap());
        log.commit().unwrap();
        let saved_bytes = log.bytes();
        assert!(!log.append(id, 42, &payload).unwrap());
        assert!(
            log.append(id, 42, &encode_start(transmission, 2).unwrap())
                .is_err()
        );
        log.commit().unwrap();
        drop(log);

        let reopened = RecordingLog::open(&path, transmission).unwrap();
        assert_eq!(reopened.bytes(), saved_bytes);
        assert_eq!(reopened.records().count(), 1);
        let record = reopened.records().next().unwrap();
        assert_eq!(record.id, id);
        assert_eq!(record.sequence, 11);
        assert_eq!(record.payload, payload);
        fs::remove_file(path).unwrap();
    }

    #[test]
    fn p3_envelope_and_partial_final_batch_share_the_same_parser() {
        let tx = [0x61; 16];
        let start = encode_start(tx, 100).unwrap();
        assert!(matches!(
            parse_message(&start),
            Ok(Message::Start(Start {
                batch_count: 100,
                ..
            }))
        ));
        let batch = encode_batch(tx, 0, &vec![vec![1; 48]; 5]).unwrap();
        assert!(
            matches!(parse_message(&batch), Ok(Message::Batch(Batch { packets, .. })) if packets.len() == 5)
        );
        let tail = encode_batch(tx, 2, &[vec![7; 48]]).unwrap();
        assert!(
            matches!(parse_message(&tail), Ok(Message::Batch(Batch { index: 2, packets, .. })) if packets.len() == 1)
        );
        assert_eq!(
            recording_identity(&tail, tx, record_id(tx, 2, 2)).unwrap(),
            (2, 2)
        );
    }
}
