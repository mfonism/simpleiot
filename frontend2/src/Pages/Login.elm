module Pages.Login exposing (Model, Msg, page)

import Element exposing (..)
import Element.Font as Font
import Element.Input as Input
import Generated.Params as Params
import Global
import Spa.Page
import Utils.Spa exposing (Page)


white =
    Element.rgb 1 1 1


grey =
    Element.rgb 0.9 0.9 0.9


blue =
    Element.rgb 0 0 0.8


red =
    Element.rgb 0.8 0 0


darkBlue =
    Element.rgb 0 0 0.9


page : Page Params.Login Model Msg model msg appMsg
page =
    Spa.Page.component
        { title = always "Login"
        , init = always init
        , update = always update
        , subscriptions = always subscriptions
        , view = always view
        }



-- INIT


type alias Model =
    { email : String
    , password : String
    }


init : Params.Login -> ( Model, Cmd Msg, Cmd Global.Msg )
init _ =
    ( { email = "", password = "" }
    , Cmd.none
    , Cmd.none
    )



-- UPDATE


type Msg
    = Update Model


update : Msg -> Model -> ( Model, Cmd Msg, Cmd Global.Msg )
update msg model =
    case msg of
        Update m ->
            ( m, Cmd.none, Cmd.none )



-- SUBSCRIPTIONS


subscriptions : Model -> Sub Msg
subscriptions model =
    Sub.none



-- VIEW


view : Model -> Element Msg
view model =
    column
        [ width (px 400)
        , spacing 12
        , centerX
        , centerY
        , spacing 36
        , padding 10
        , height shrink
        ]
        [ Input.email
            [ spacing 12 ]
            { text = model.email
            , placeholder = Just (Input.placeholder [] (text "email"))
            , onChange = \new -> Update { model | email = new }
            , label = Input.labelAbove [ Font.size 14 ] (text "Username")
            }
        , Input.currentPassword
            [ spacing 12 ]
            { text = model.password
            , placeholder = Just (Input.placeholder [] (text "password"))
            , onChange = \new -> Update { model | password = new }
            , label = Input.labelAbove [ Font.size 14 ] (text "Password")
            , show = False
            }
        ]