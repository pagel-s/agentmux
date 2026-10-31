"""Small intentionally incomplete implementation for the mixed-team smoke."""


def normalize_tags(values):
    """Return normalized unique tags; the acceptance task specifies the rules."""
    return sorted(set(values))
