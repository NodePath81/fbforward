import type { NotificationEvent, NotificationSource, Severity } from './types';

const IDENTIFIER_PATTERN = /^[a-zA-Z0-9._:-]{1,128}$/;

function validateIdentifier(value: unknown, label: string): string | null {
  if (typeof value !== 'string') {
    return `${label} must be a string`;
  }
  const trimmed = value.trim();
  if (trimmed === '') {
    return `${label} is required`;
  }
  if (!IDENTIFIER_PATTERN.test(trimmed)) {
    return `${label} contains unsupported characters`;
  }
  return null;
}

function validateSeverity(value: unknown): Severity | null {
  if (value === 'info' || value === 'warn' || value === 'critical') {
    return value;
  }
  return null;
}

function validateTimestamp(value: unknown): string | number | null {
  if (typeof value === 'string' && value.trim() !== '') {
    return value;
  }
  if (typeof value === 'number' && Number.isFinite(value)) {
    return value;
  }
  return null;
}

function validateSource(value: unknown): { source: NotificationSource | null; error: string | null } {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return { source: null, error: 'source must be an object' };
  }
  const source = value as Record<string, unknown>;
  const serviceError = validateIdentifier(source.service, 'source.service');
  if (serviceError) {
    return { source: null, error: serviceError };
  }
  const instanceError = validateIdentifier(source.instance, 'source.instance');
  if (instanceError) {
    return { source: null, error: instanceError };
  }
  return {
    source: {
      service: String(source.service).trim(),
      instance: String(source.instance).trim()
    },
    error: null
  };
}

export function validateNotificationEvent(value: unknown): { event: NotificationEvent | null; error: string | null } {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return { event: null, error: 'event body must be a JSON object' };
  }
  const record = value as Record<string, unknown>;
  if (typeof record.schema_version !== 'number' || !Number.isInteger(record.schema_version) || record.schema_version < 1) {
    return { event: null, error: 'schema_version must be a positive integer' };
  }
  const nameError = validateIdentifier(record.event_name, 'event_name');
  if (nameError) {
    return { event: null, error: nameError };
  }
  const severity = validateSeverity(record.severity);
  if (!severity) {
    return { event: null, error: 'severity must be one of info, warn, critical' };
  }
  const timestamp = validateTimestamp(record.timestamp);
  if (timestamp === null) {
    return { event: null, error: 'timestamp must be a string or number' };
  }
  const { source, error: sourceError } = validateSource(record.source);
  if (sourceError || !source) {
    return { event: null, error: sourceError ?? 'source is invalid' };
  }
  if (!record.attributes || typeof record.attributes !== 'object' || Array.isArray(record.attributes)) {
    return { event: null, error: 'attributes must be an object' };
  }

  return {
    event: {
      schema_version: record.schema_version,
      event_name: String(record.event_name).trim(),
      severity,
      timestamp,
      source,
      attributes: record.attributes as Record<string, unknown>
    },
    error: null
  };
}

export function validateTargetIdentifier(value: unknown, label: string): string | null {
  return validateIdentifier(value, label);
}
