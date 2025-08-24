"""Central logging configuration for Yuna."""

import logging
import sys


def setup_logging(log_file: str = "yuna.log") -> None:
    """Configures the root logger for the application.

    Creates a console handler and a rotating file handler (single file).
    """
    logger = logging.getLogger()
    logger.setLevel(logging.INFO)

    # Avoid adding multiple handlers if setup_logging is called more than once
    if logger.handlers:
        return

    formatter = logging.Formatter(
        "%(asctime)s - %(name)s - %(levelname)s - %(message)s"
    )

    stream_handler = logging.StreamHandler(sys.stdout)
    stream_handler.setFormatter(formatter)
    logger.addHandler(stream_handler)

    file_handler = logging.FileHandler(log_file)
    file_handler.setFormatter(formatter)
    logger.addHandler(file_handler)
