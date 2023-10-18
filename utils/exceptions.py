import traceback

from flask import jsonify
from werkzeug.exceptions import (BadRequest, Forbidden, InternalServerError,
                                 PreconditionFailed, Unauthorized)


"""
These Exception classes can be used to raise standard HTTP based exceptions with a built-in JSON response.
"""


class HttpBasedException():
    pass


class BadRequestException(BadRequest, HttpBasedException):
    def __init__(self, message=None, code=None, additional_fields=None, *args, **kwargs):
        self.message = message if message is not None else 'Bad Request'
        self.code = code if code is not None else 400
        self.additional_fields = additional_fields

        self.response = create_json_response(self.message, code=self.code, http_code=400, additional_fields=self.additional_fields)

        super().__init__(response=self.response, *args, **kwargs)


class ForbiddenException(Forbidden, HttpBasedException):
    def __init__(self, message=None, code=None, additional_fields=None, *args, **kwargs):
        self.message = message if message is not None else 'Forbidden'
        self.code = code if code is not None else 403
        self.additional_fields = additional_fields

        self.response = create_json_response(self.message, code=self.code, http_code=403, additional_fields=self.additional_fields)

        super().__init__(response=self.response, *args, **kwargs)


class InternalServerErrorException(InternalServerError, HttpBasedException):
    def __init__(self, message=None, code=None, additional_fields=None, *args, **kwargs):
        self.message = message if message is not None else 'Internal Server Error'
        self.code = code if code is not None else 500
        self.additional_fields = additional_fields

        self.response = create_json_response(self.message, code=self.code, http_code=500, additional_fields=self.additional_fields)

        super().__init__(response=self.response, *args, **kwargs)


class UnauthorizedException(Unauthorized, HttpBasedException):
    def __init__(self, message=None, code=None, additional_fields=None, *args, **kwargs):
        self.message = message if message is not None else 'Unauthorized'
        self.code = code if code is not None else 401
        self.additional_fields = additional_fields

        self.response = create_json_response(self.message, code=self.code, http_code=401, additional_fields=self.additional_fields)

        super().__init__(response=self.response, *args, **kwargs)


class NotFoundException(BadRequest, HttpBasedException):
    def __init__(self, message=None, code=None, additional_fields=None, *args, **kwargs):
        self.message = message if message is not None else 'Not Found'
        self.code = code if code is not None else 404
        self.additional_fields = additional_fields

        self.response = create_json_response(self.message, code=self.code, http_code=404, additional_fields=self.additional_fields)

        super().__init__(response=self.response, *args, **kwargs)


class PreconditionFailedException(PreconditionFailed, HttpBasedException):
    def __init__(self, message=None, code=None, additional_fields=None, *args, **kwargs):
        self.message = message if message is not None else 'Precondition Failed'
        self.code = code if code is not None else 412
        self.additional_fields = additional_fields

        self.response = create_json_response(self.message, code=self.code, http_code=412, additional_fields=self.additional_fields)

        super().__init__(response=self.response, *args, **kwargs)


def create_json_response(message, code, http_code, additional_fields):
    content = {
        "code": code,
        "message": message
    }

    if additional_fields:
        content.update(additional_fields)

    # if _config.get('env') in ['development', 'testing']:
    #     content['traceback'] = "".join(traceback.format_exc())

    resp = jsonify(content)
    resp.status_code = http_code

    return resp